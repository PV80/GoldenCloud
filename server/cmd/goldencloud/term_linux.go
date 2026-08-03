//go:build linux

package main

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"syscall"
	"unsafe"
)

// errNotATerminal signals that the caller should fall back to a plain read.
var errNotATerminal = errors.New("not a terminal")

// readPasswordNoEcho reads one line from fd with terminal echo disabled, so a
// password typed at the console does not appear on screen or in a scrollback
// buffer. golang.org/x/term would do this too, but it is a fourth dependency
// (see D-001) for something that is two ioctls.
func readPasswordNoEcho(fd int) ([]byte, error) {
	var orig syscall.Termios
	if err := ioctlTermios(fd, syscall.TCGETS, &orig); err != nil {
		return nil, errNotATerminal
	}
	noEcho := orig
	noEcho.Lflag &^= syscall.ECHO
	noEcho.Lflag |= syscall.ICANON | syscall.ISIG
	if err := ioctlTermios(fd, syscall.TCSETS, &noEcho); err != nil {
		return nil, err
	}
	defer func() { _ = ioctlTermios(fd, syscall.TCSETS, &orig) }()

	line, err := bufio.NewReader(os.NewFile(uintptr(fd), "/dev/stdin")).ReadString('\n')
	if err != nil && line == "" {
		return nil, err
	}
	return bytes.TrimRight([]byte(line), "\r\n"), nil
}

func ioctlTermios(fd int, req uintptr, t *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(t)))
	if errno != 0 {
		return errno
	}
	return nil
}
