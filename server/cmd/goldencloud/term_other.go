//go:build !linux

package main

import "errors"

// errNotATerminal signals that the caller should fall back to a plain read.
var errNotATerminal = errors.New("not a terminal")

// readPasswordNoEcho is unimplemented off Linux; callers fall back to a plain
// read with a warning. GoldenCloud only ships linux/amd64 and linux/arm64
// binaries, so this exists to keep the package building elsewhere for
// development.
func readPasswordNoEcho(int) ([]byte, error) { return nil, errNotATerminal }
