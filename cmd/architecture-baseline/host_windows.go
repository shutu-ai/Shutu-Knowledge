//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func diskCapacity(path string) (available, total uint64) {
	var free, capacity, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(path), &free, &capacity, &totalFree); err != nil {
		return 0, 0
	}
	return free, capacity
}

func hostMemoryBytes() uint64 {
	type memoryStatusEx struct {
		Length     uint32
		MemoryLoad uint32
		TotalPhys  uint64
		AvailPhys  uint64
		TotalPage  uint64
		AvailPage  uint64
		TotalVirt  uint64
		AvailVirt  uint64
		AvailExt   uint64
	}
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	result, _, _ := proc.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return 0
	}
	return status.TotalPhys
}
