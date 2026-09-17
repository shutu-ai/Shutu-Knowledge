//go:build windows

package operations

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type statusProcessMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

var processMemoryAPI = windows.NewLazySystemDLL("psapi.dll")
var processMemoryInfo = processMemoryAPI.NewProc("GetProcessMemoryInfo")

// CurrentProcessRSS returns the Windows current working set in bytes. The
// status field records it as a sample; acceptance tests separately sample
// the peak working set over the scenario.
func CurrentProcessRSS() uint64 {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return 0
	}
	var counters statusProcessMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))
	result, _, _ := processMemoryInfo.Call(
		uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB),
	)
	if result == 0 {
		return 0
	}
	return uint64(counters.WorkingSetSize)
}
