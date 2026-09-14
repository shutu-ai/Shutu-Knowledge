package operations

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processMemoryCounters struct {
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

var psapi = windows.NewLazySystemDLL("psapi.dll")
var getProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")

func currentProcessRSS() (uint64, uint64, error) {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, 0, err
	}
	var counters processMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))
	result, callErr, _ := getProcessMemoryInfo.Call(
		uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB),
	)
	if result == 0 {
		return 0, 0, fmt.Errorf("GetProcessMemoryInfo: %v", callErr)
	}
	return uint64(counters.WorkingSetSize), uint64(counters.PeakWorkingSetSize), nil
}
