//go:build !windows

package operations

import (
	"runtime"
	"syscall"
)

// CurrentProcessRSS returns the process peak resident set size. Unix kernels
// report ru_maxrss in KiB on Linux and bytes on macOS/BSD.
func CurrentProcessRSS() uint64 {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil {
		return 0
	}
	value := uint64(usage.Maxrss)
	if runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" && runtime.GOOS != "openbsd" && runtime.GOOS != "netbsd" {
		value *= 1024
	}
	return value
}
