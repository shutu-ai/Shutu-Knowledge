//go:build !windows

package operations

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ps reports current RSS on Unix hosts. Peak RSS is sampled by the caller and
// therefore recorded as the observed peak rather than the kernel high-water mark.
func currentProcessRSS() (uint64, uint64, error) {
	output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0, 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse rss: %w", err)
	}
	return value * 1024, 0, nil
}
