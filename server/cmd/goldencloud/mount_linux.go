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

// nearestMountpoint returns the mount point of the filesystem that path lives
// on: path itself if it is a mountpoint, otherwise the closest ancestor that
// is, and "/" if nothing closer is mounted.
//
// This backs D-008. D-010 puts storage_root inside the share
// (/mnt/wd/goldencloud) rather than at it, so "is storage_root a mountpoint"
// would be the wrong question; "which filesystem does storage_root live on" is
// the right one. If the answer is the root filesystem when the operator said
// the storage is a mounted share, the share is not mounted and the server must
// refuse to start rather than fill the Pi's SD card.
//
// /proc/self/mountinfo is authoritative and, unlike comparing device numbers,
// also catches bind mounts within a single filesystem. Device comparison is the
// fallback for the case where /proc is not mounted.
func nearestMountpoint(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)

	mounts, mountsErr := mountpointSet()
	for cur := abs; ; cur = filepath.Dir(cur) {
		if cur == string(filepath.Separator) {
			return cur, nil
		}
		if mountsErr == nil {
			if mounts[cur] {
				return cur, nil
			}
			continue
		}
		// Fallback: a directory whose device differs from its parent's is a
		// mountpoint.
		diff, err := differentDeviceFromParent(cur)
		if err != nil {
			return "", err
		}
		if diff {
			return cur, nil
		}
	}
}

// isMountpoint reports whether path is itself the root of a mounted filesystem.
func isMountpoint(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)
	mp, err := nearestMountpoint(abs)
	if err != nil {
		return false, err
	}
	return mp == abs, nil
}

// mountpointSet reads every mount point out of /proc/self/mountinfo.
func mountpointSet() (map[string]bool, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		// 36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw
		//                        ^ field 5 (index 4) is the mount point
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		out[unescapeMountinfo(fields[4])] = true
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
