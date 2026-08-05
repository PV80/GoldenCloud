//go:build !unix

package config

import "os"

// preserveOwner is a no-op on platforms without Unix file ownership. The server
// is deployed only on Linux; this exists so the binary still builds on, say, a
// Windows laptop used to try GoldenCloud locally.
func preserveOwner(_ *os.File, _ string) {}

// syncDir is a no-op off Unix. Windows does not permit fsync on a directory
// handle (it fails with "Access is denied"); directory-entry durability there
// is the filesystem's responsibility, not an explicit dir fsync.
func syncDir(string) error { return nil }
