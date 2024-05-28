// Copyright (c) 2019 FOSS contributors of https://github.com/bloominlabs/tail
// Copyright (c) 2015 HPE Software Inc. All rights reserved.
// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package watch

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/bloominlabs/tail/util"
	"gopkg.in/tomb.v1"
)

type PollingFileWatcherOption func(*PollingFileWatcher)

func WithPollDuration(value time.Duration) PollingFileWatcherOption {
	return func(c *PollingFileWatcher) {
		c.PollDuration = value
	}
}

// PollingFileWatcher polls the file for changes.
type PollingFileWatcher struct {
	Filename     string
	Size         int64
	PollDuration time.Duration
}

func NewPollingFileWatcher(filename string, options ...PollingFileWatcherOption) *PollingFileWatcher {
	fw := &PollingFileWatcher{filename, 0, 250 * time.Millisecond}
	for _, o := range options {
		o(fw)
	}
	fmt.Println(fw)
	return fw
}

func (fw *PollingFileWatcher) BlockUntilExists(t *tomb.Tomb) error {
	for {
		if _, err := os.Stat(fw.Filename); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		select {
		case <-time.After(fw.PollDuration):
			continue
		case <-t.Dying():
			return tomb.ErrDying
		}
	}
	panic("unreachable")
}

func (fw *PollingFileWatcher) ChangeEvents(t *tomb.Tomb, pos int64) (*FileChanges, error) {
	origFi, err := os.Stat(fw.Filename)
	if err != nil {
		return nil, err
	}

	changes := NewFileChanges()
	var prevModTime time.Time

	// XXX: use tomb.Tomb to cleanly manage these goroutines. replace
	// the fatal (below) with tomb's Kill.

	fw.Size = pos

	go func() {
		prevSize := fw.Size
		for {
			select {
			case <-t.Dying():
				return
			default:
			}

			time.Sleep(fw.PollDuration)
			fi, err := os.Stat(fw.Filename)
			if err != nil {
				// Windows cannot delete a file if a handle is still open (tail keeps one open)
				// so it gives access denied to anything trying to read it until all handles are released.
				if os.IsNotExist(err) || (runtime.GOOS == "windows" && os.IsPermission(err)) {
					// File does not exist (has been deleted).
					changes.NotifyDeleted()
					return
				}

				// XXX: report this error back to the user
				util.Fatal("Failed to stat file %v: %v", fw.Filename, err)
			}

			// File got moved/renamed?
			if !os.SameFile(origFi, fi) {
				changes.NotifyDeleted()
				return
			}

			// File got truncated?
			fw.Size = fi.Size()
			if prevSize > 0 && prevSize > fw.Size {
				changes.NotifyTruncated()
				prevSize = fw.Size
				continue
			}
			// File got bigger?
			if prevSize > 0 && prevSize < fw.Size {
				changes.NotifyModified()
				prevSize = fw.Size
				continue
			}
			prevSize = fw.Size

			// File was appended to (changed)?
			modTime := fi.ModTime()
			if modTime != prevModTime {
				prevModTime = modTime
				changes.NotifyModified()
			}
		}
	}()

	return changes, nil
}
