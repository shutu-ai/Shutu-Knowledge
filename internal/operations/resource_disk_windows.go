//go:build windows

package operations

import (
	"golang.org/x/sys/windows"
	"path/filepath"
)

func currentDiskFreeBytes(path string) (uint64, error) {
	if path == "" {
		path = "."
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	directory, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(directory, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}
