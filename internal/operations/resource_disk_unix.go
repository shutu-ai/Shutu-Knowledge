//go:build !windows

package operations

import (
	"path/filepath"
	"syscall"
)

func currentDiskFreeBytes(path string) (uint64, error) {
	if path == "" {
		path = "."
	}
	path = filepath.Dir(path)
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
