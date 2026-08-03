//go:build !unix

package config

import "os"

// preserveOwner is a no-op on platforms without Unix file ownership. The server
// is deployed only on Linux; this exists so the binary still builds on, say, a
// Windows laptop used to try GoldenCloud locally.
func preserveOwner(_ *os.File, _ string) {}
