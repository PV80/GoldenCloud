//go:build integration

package integration

import (
	"os"
	"syscall"
)

var (
	// syscallSIGHUP asks the server to re-read users.yaml.
	syscallSIGHUP os.Signal = syscall.SIGHUP
	// syscallSignal0 delivers nothing and is the usual liveness probe.
	syscallSignal0 os.Signal = syscall.Signal(0)
)
