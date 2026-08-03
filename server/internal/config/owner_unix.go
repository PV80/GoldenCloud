//go:build unix

package config

import (
	"os"
	"syscall"
)

// preserveOwner gives tmp the same uid/gid as the existing file at dst, so an
// atomic rewrite of a root-owned users.yaml does not silently reassign it to
// whoever ran the command. Best effort: only root can hand a file to another
// owner, and a non-root operator editing their own file does not need to.
func preserveOwner(tmp *os.File, dst string) {
	st, err := os.Stat(dst)
	if err != nil {
		return
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	_ = tmp.Chown(int(sys.Uid), int(sys.Gid))
}
