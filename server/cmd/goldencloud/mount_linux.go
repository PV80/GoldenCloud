//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// isMountpoint reports whether path is the root of a mounted filesystem.
//
// This backs D-008: the WD share is mounted by the OS, and if it is missing at
// boot the server must refuse to start rather than quietly create user folders
// on the Pi's SD card, which would look like working software while putting
// everyone's files in the wrong place.
//
// /proc/self/mountinfo is authoritative and, unlike comparing device numbers,
// also catches bind mounts within a single filesystem. Device comparison is the
// fallback for the case where /proc is not mounted.
func isMountpoint(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)
	if abs == string(filepath.Separator) {
		return true, nil
	}
	if found, err := mountinfoLists(abs); err == nil {
		if found {
			return true, nil
		}
	}
	return differentDeviceFromParent(abs)
}

// mountinfoLists reports whether abs appears as a mount point in
// /proc/self/mountinfo.
func mountinfoLists(abs string) (bool, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		// 36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw
		//                        ^ field 5 is the mount point
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		if unescapeMountinfo(fields[4]) == abs {
			return true, nil
		}
	}
	return false, sc.Err()
}

// unescapeMountinfo decodes the octal escapes the kernel uses for space, tab,
// newline and backslash in mountinfo paths.
func unescapeMountinfo(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// differentDeviceFromParent reports whether abs sits on a different device from
// its parent directory, the classic mountpoint test.
func differentDeviceFromParent(abs string) (bool, error) {
	var self, parent syscall.Stat_t
	if err := syscall.Stat(abs, &self); err != nil {
		return false, fmt.Errorf("stat %s: %w", abs, err)
	}
	if err := syscall.Stat(filepath.Dir(abs), &parent); err != nil {
		return false, fmt.Errorf("stat %s: %w", filepath.Dir(abs), err)
	}
	return self.Dev != parent.Dev, nil
}
