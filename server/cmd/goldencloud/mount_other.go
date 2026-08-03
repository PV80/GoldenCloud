//go:build !linux

package main

import "errors"

var errMountUnsupported = errors.New("require_mountpoint is only supported on Linux; set it to false")

// nearestMountpoint is only implemented on Linux, which is the only platform
// GoldenCloud servers run on. An operator who sets require_mountpoint elsewhere
// gets an explicit refusal rather than a check that silently passes.
func nearestMountpoint(string) (string, error) { return "", errMountUnsupported }

// isMountpoint is likewise Linux-only.
func isMountpoint(string) (bool, error) { return false, errMountUnsupported }
