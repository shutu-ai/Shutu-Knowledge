package operations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const filesystemResourceSampleInterval = 2 * time.Second

// ResourceSnapshot is a bounded, data-free view of the process and durable
// storage footprint. It is sampled for status/acceptance reporting; it never
// contains command payloads or source content.
type ResourceSnapshot struct {
	PeakRSSBytes  uint64 `json:"peakRssBytes"`
	HeapBytes     uint64 `json:"heapBytes"`
	DatabaseBytes int64  `json:"databaseBytes"`
	WALBytes      int64  `json:"walBytes"`
	RawBytes      int64  `json:"rawBytes"`
	TempBytes     int64  `json:"tempBytes"`
	UploadBytes   int64  `json:"uploadBytes"`
	DiskBytes     int64  `json:"diskBytes"`
	DiskFreeBytes uint64 `json:"diskFreeBytes"`
}

// ResourceSampler supplies the process/storage measurements exposed by the
// scheduler status endpoint.
type ResourceSampler func() ResourceSnapshot

// DiskFreeSampler reports free bytes on the database volume. It is separate
// from ResourceSampler so admission need not walk a raw corpus.
type DiskFreeSampler func() uint64

// NewFilesystemResourceSampler returns a cheap sampler for the database and
// staging roots owned by one App. Temporary files are counted by name so a
// large stable raw corpus is not misreported as temporary pressure.
func NewFilesystemResourceSampler(databasePath, rawRoot, uploadRoot string) ResourceSampler {
	return newFilesystemResourceSampler(databasePath, rawRoot, uploadRoot, filesystemResourceSampleInterval)
}

func newFilesystemResourceSampler(databasePath, rawRoot, uploadRoot string, interval time.Duration) ResourceSampler {
	var mu sync.Mutex
	var cached ResourceSnapshot
	var sampledAt time.Time
	return func() ResourceSnapshot {
		mu.Lock()
		defer mu.Unlock()
		nowAt := time.Now()
		if !sampledAt.IsZero() && interval > 0 && nowAt.Sub(sampledAt) < interval {
			return refreshCheapResourceFields(cached, databasePath)
		}

		var rawTemp, uploadTemp int64
		cached.RawBytes, rawTemp = directoryFootprint(rawRoot)
		cached.UploadBytes, uploadTemp = directoryFootprint(uploadRoot)
		cached.TempBytes = rawTemp + uploadTemp
		sampledAt = nowAt
		return refreshCheapResourceFields(cached, databasePath)
	}
}

func refreshCheapResourceFields(snapshot ResourceSnapshot, databasePath string) ResourceSnapshot {
	snapshot.PeakRSSBytes = CurrentProcessRSS()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	snapshot.HeapBytes = memory.HeapAlloc
	snapshot.DatabaseBytes = fileSize(databasePath)
	snapshot.WALBytes = fileSize(databasePath + "-wal")
	snapshot.DiskBytes = snapshot.DatabaseBytes + snapshot.WALBytes + snapshot.RawBytes + snapshot.UploadBytes
	snapshot.DiskFreeBytes, _ = currentDiskFreeBytes(databasePath)
	return snapshot
}

// NewFilesystemDiskFreeSampler returns a cheap volume-capacity probe. A zero
// result means the platform probe was unavailable.
func NewFilesystemDiskFreeSampler(databasePath string) DiskFreeSampler {
	return func() uint64 {
		free, _ := currentDiskFreeBytes(databasePath)
		return free
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

func directoryFootprint(root string) (size, temp int64) {
	if strings.TrimSpace(root) == "" {
		return 0, 0
	}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.Mode().IsRegular() {
			return nil
		}
		size += info.Size()
		name := info.Name()
		if strings.Contains(name, ".tmp-") || strings.HasSuffix(name, ".tmp") {
			temp += info.Size()
		}
		return nil
	})
	return size, temp
}
