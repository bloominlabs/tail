[![Go Reference](https://pkg.go.dev/badge/github.com/bloominlabs/tail.svg)](https://pkg.go.dev/github.com/bloominlabs/tail#section-documentation)
![ci](https://github.com/bloominlabs/tail/workflows/ci/badge.svg)
[![FreeBSD](https://api.cirrus-ci.com/github/bloominlabs/tail.svg)](https://cirrus-ci.com/github/bloominlabs/tail)
# tail functionality in Go

bloominlabs/tail provides a Go library that emulates the features of the BSD `tail`
program. The library comes with full support for truncation/move detection as
it is designed to work with log rotation tools. The library works on all
operating systems supported by Go, including POSIX systems like Linux, *BSD,
MacOS, and MS Windows. Go 1.12 is the oldest compiler release supported.

A simple example:

```Go
// Create a tail
t, err := tail.TailFile(
	"/var/log/nginx.log", tail.Config{Follow: true, ReOpen: true})
if err != nil {
    panic(err)
}

// Print the text of each received line
for line := range t.Lines {
    fmt.Println(line.Text)
}
```

See [API documentation](https://pkg.go.dev/github.com/bloominlabs/tail#section-documentation).

## Installing

    go get github.com/bloominlabs/tail/...

## History

This project is an active, drop-in replacement for the
[abandoned](https://en.wikipedia.org/wiki/HPE_Helion) Go tail library at
[hpcloud](https://github.com/hpcloud/tail). Next to
[addressing open issues/PRs of the original project](https://github.com/bloominlabs/tail/issues/6),
bloominlabs/tail continues the development by keeping up to date with the Go toolchain
(e.g. go modules) and dependencies, completing the documentation, adding features
and fixing bugs.

## Examples
Examples, e.g. used to debug an issue, are kept in the [examples directory](/examples).
