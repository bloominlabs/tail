// Copyright (c) 2019 FOSS contributors of https://github.com/bloominlabs/tail
// Copyright (c) 2015 HPE Software Inc. All rights reserved.
// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
	"golang.org/x/exp/slices"
)

type InotifyTracker struct {
	sync.RWMutex

	watcher           *fsnotify.Watcher
	broadcastChannels map[string][]chan fsnotify.Event
	closeChannels     map[string][]chan bool

	addWatcherCh    chan Watcher
	removeWatcherCh chan Watcher
	cleanupCh       chan string
	errorCh         chan error
}

type Watcher struct {
	eventsChan chan fsnotify.Event
	filename   string
}

var (
	// globally shared InotifyTracker; ensures only one fsnotify.Watcher is used
	shared *InotifyTracker

	logger = log.With().Caller().Logger()

	// these are used to ensure the shared InotifyTracker is run exactly once
	once  = sync.Once{}
	goRun = func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			logger.Fatal().Err(err).Msg("failed to create Watcher")
		}
		shared = &InotifyTracker{
			broadcastChannels: make(map[string][]chan fsnotify.Event),
			closeChannels:     make(map[string][]chan bool),
			addWatcherCh:      make(chan Watcher),
			removeWatcherCh:   make(chan Watcher),
			cleanupCh:         make(chan string),
			errorCh:           make(chan error),
			watcher:           watcher,
		}

		go shared.run()
	}
)

// Watch signals the run goroutine to begin watching the input filename
func CreateWatcher(filename string) (Watcher, error) {
	once.Do(goRun)

	watcher := Watcher{
		filename:   filepath.Clean(filename),
		eventsChan: make(chan fsnotify.Event),
	}

	shared.addWatcherCh <- watcher

	return watcher, <-shared.errorCh
}

func RemoveWatcher(watcher Watcher) error {
	once.Do(goRun)

	// We close the done channel and then send the inotify_tracker a signal to
	// delete the watcher to prevent a race condition where we are broadcasting
	// an event + removing a watcher simultaneously sends a message over a closed channel.
	//
	// By closing the 'doneChannel' and then **signaling** the inotify_tracker
	// tracker its ready to remove a watcher, it will allow any ongoing broadcast
	// to complete before we close the channel. Next time we try to broadcast, we
	// will get this close event instead and exit without trying to send if we're
	// trying to broadcast at the same time.
	shared.Lock()
	broadcastIndex := slices.Index(shared.broadcastChannels[watcher.filename], watcher.eventsChan)
	if broadcastIndex == -1 {
		shared.Unlock()
		// TODO
		return fmt.Errorf("could not find broadcast channel index")
	}
	if len(shared.closeChannels[watcher.filename]) > broadcastIndex {
		closeCh := shared.closeChannels[watcher.filename][broadcastIndex]
		close(closeCh)
		shared.closeChannels[watcher.filename] = slices.Delete(shared.closeChannels[watcher.filename], broadcastIndex, broadcastIndex+1)
	}
	shared.Unlock()

	shared.removeWatcherCh <- watcher
	return <-shared.errorCh
}

func Cleanup(filename string) error {
	once.Do(goRun)

	// We close the done channel and then send the inotify_tracker a signal to
	// delete the watcher to prevent a race condition where we are broadcasting
	// an event + removing a watcher simultaneously sends a message over a closed channel.
	//
	// By closing the 'doneChannel' and then **signaling** the inotify_tracker
	// tracker its ready to remove a watcher, it will allow any ongoing broadcast
	// to complete before we close the channel. Next time we try to broadcast, we
	// will get this close event instead and exit without trying to send if we're
	// trying to broadcast at the same time.
	shared.Lock()
	closeChs := shared.closeChannels[filename]
	for _, ch := range closeChs {
		close(ch)
	}
	delete(shared.closeChannels, filename)
	shared.Unlock()

	shared.cleanupCh <- filename
	return <-shared.errorCh
}

// sendEvent sends the input event to the appropriate Tail.
func (shared *InotifyTracker) broadcastEvent(event fsnotify.Event) error {
	name := filepath.Clean(event.Name)
	parent := filepath.Dir(name)

	shared.RLock()
	channels := append(shared.broadcastChannels[name], shared.broadcastChannels[parent]...)
	doneChs := append(shared.closeChannels[name], shared.closeChannels[parent]...)
	shared.RUnlock()

	for index, ch := range channels {
		if len(doneChs) > index {
			select {
			case <-doneChs[index]:
			case ch <- event:
			}
		}
	}

	return nil
}

func (shared *InotifyTracker) removeWatcher(watcher Watcher) error {
	shared.Lock()

	broadcastIndex := slices.Index(shared.broadcastChannels[watcher.filename], watcher.eventsChan)
	if broadcastIndex == -1 {
		shared.Unlock()
		// TODO
		return fmt.Errorf("could not find broadcast channel index")
	}
	ch := shared.broadcastChannels[watcher.filename][broadcastIndex]
	close(ch)
	shared.broadcastChannels[watcher.filename] = slices.Delete(shared.broadcastChannels[watcher.filename], broadcastIndex, broadcastIndex+1)
	shared.Unlock()

	// If we were the last ones to watch this file, unsubscribe from inotify.
	// This needs to happen after releasing the lock because fsnotify waits
	// synchronously for the kernel to acknowledge the removal of the watch
	// for this file, which causes us to deadlock if we still held the lock.
	if len(shared.broadcastChannels[watcher.filename]) == 0 {
		if err := shared.watcher.Remove(watcher.filename); err != nil {
			return fmt.Errorf("could not remove channel from fsnotify watcher: %w", err)
		}
	}

	return nil
}

func (shared *InotifyTracker) addWatcher(watcher Watcher) error {
	shared.Lock()
	defer shared.Unlock()
	if shared.broadcastChannels[watcher.filename] == nil {
		shared.broadcastChannels[watcher.filename] = make([]chan fsnotify.Event, 0)
	}
	if shared.closeChannels[watcher.filename] == nil {
		shared.closeChannels[watcher.filename] = make([]chan bool, 0)
	}

	shared.broadcastChannels[watcher.filename] = append(shared.broadcastChannels[watcher.filename], watcher.eventsChan)
	shared.closeChannels[watcher.filename] = append(shared.closeChannels[watcher.filename], make(chan bool))

	if len(shared.broadcastChannels[watcher.filename]) <= 1 {
		if err := shared.watcher.Add(watcher.filename); err != nil {
			return fmt.Errorf("failed to add watcher: %w", err)
		}
	}

	return nil
}

func (shared *InotifyTracker) cleanup(filename string) error {
	shared.Lock()
	chans := shared.broadcastChannels[filename]
	for _, ch := range chans {
		close(ch)
	}

	delete(shared.broadcastChannels, filename)
	shared.Unlock()

	// If we were the last ones to watch this file, unsubscribe from inotify.
	// This needs to happen after releasing the lock because fsnotify waits
	// synchronously for the kernel to acknowledge the removal of the watch
	// for this file, which causes us to deadlock if we still held the lock.
	shared.watcher.Remove(filename)

	return nil
}

// run starts the goroutine in which the shared struct reads events from its
// Watcher's Event channel and sends the events to the appropriate Tail.
func (shared *InotifyTracker) run() {
	for {
		select {
		case watcher := <-shared.addWatcherCh:
			shared.errorCh <- shared.addWatcher(watcher)
		case watcher := <-shared.removeWatcherCh:
			shared.errorCh <- shared.removeWatcher(watcher)

		case fname := <-shared.cleanupCh:
			shared.errorCh <- shared.cleanup(fname)

		case event, open := <-shared.watcher.Events:
			if !open {
				return
			}
			shared.broadcastEvent(event)

		case err, open := <-shared.watcher.Errors:
			if !open {
				return
			} else if err != nil {
				sysErr, ok := err.(*os.SyscallError)
				if !ok || sysErr.Err != syscall.EINTR {
					logger.Error().Err(err).Msg("error in watch error channel")
				}
			}
		}
	}
}
