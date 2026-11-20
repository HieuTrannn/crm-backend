//go:build integration

package integration

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HieuTrannn/crm-backend/internal/importer"
)

// BenchmarkImport100k measures NFR-PERF-02: 100,000 CSV lines (5% invalid)
// through the real service and PostgreSQL. Run it with:
//
//	IMPORT_HEAP_PROFILE=heap.pprof go test -tags=integration -run '^$' \
//	  -bench BenchmarkImport100k -benchtime=1x ./test/integration/
//
// It reports wall time, the peak growth of the Go heap in use and of the memory
// the process got from the OS while the import ran, and writes a heap profile.
func BenchmarkImport100k(b *testing.B) {
	const rows = 100_000
	e := setup(b)
	s := seed(b, e)
	jobs := importer.NewJobManager()
	b.Cleanup(func() { _ = jobs.Shutdown(context.Background()) })
	svc := importer.NewService(importer.NewPostgresRepository(e.db), jobs, runtime.NumCPU(), slog.New(slog.DiscardHandler))

	var csv bytes.Buffer
	invalid, err := importer.GenerateCSV(&csv, rows)
	require.NoError(b, err)

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	peakHeap, peakSys := sampleMemory(b)

	b.ResetTimer()
	start := time.Now()
	job, err := svc.Start(b.Context(), s.tenantA, s.userA, bytes.NewReader(csv.Bytes()))
	require.NoError(b, err)
	for job.Status == importer.StatusPending || job.Status == importer.StatusRunning {
		time.Sleep(50 * time.Millisecond)
		job, err = svc.Get(b.Context(), s.tenantA, job.ID)
		require.NoError(b, err)
	}
	elapsed := time.Since(start)
	b.StopTimer()
	heap, sys := peakHeap(), peakSys()

	require.Equal(b, importer.StatusCompleted, job.Status)
	require.Equal(b, rows, int(job.TotalRows))
	require.Equal(b, rows-invalid, int(job.SuccessRows))
	require.Equal(b, invalid, int(job.ErrorRows))

	const mb = 1 << 20
	b.ReportMetric(elapsed.Seconds(), "s/import")
	b.ReportMetric(float64(rows)/elapsed.Seconds(), "rows/s")
	b.ReportMetric(float64(heap-base.HeapInuse)/mb, "MB-heap-growth")
	b.ReportMetric(float64(sys-base.Sys)/mb, "MB-sys-growth")
	b.Logf("workers=%d rows=%d invalid=%d elapsed=%s", runtime.NumCPU(), rows, invalid, elapsed)

	if path := os.Getenv("IMPORT_HEAP_PROFILE"); path != "" {
		f, err := os.Create(path) //nolint:gosec // developer-chosen output path
		require.NoError(b, err)
		require.NoError(b, pprof.WriteHeapProfile(f))
		require.NoError(b, f.Close())
	}
}

// sampleMemory records the highest HeapInuse and Sys every 20 ms until the
// benchmark ends; the returned functions stop sampling and read the peaks.
func sampleMemory(b *testing.B) (heap, sys func() uint64) {
	var (
		mu              sync.Mutex
		maxHeap, maxSys uint64
		stop            = make(chan struct{})
		done            = make(chan struct{})
		once            sync.Once
		finish          = func() { once.Do(func() { close(stop); <-done }) }
	)
	go func() {
		defer close(done)
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			mu.Lock()
			maxHeap, maxSys = max(maxHeap, m.HeapInuse), max(maxSys, m.Sys)
			mu.Unlock()
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()
	b.Cleanup(finish)
	read := func(v *uint64) func() uint64 {
		return func() uint64 {
			finish()
			mu.Lock()
			defer mu.Unlock()
			return *v
		}
	}
	return read(&maxHeap), read(&maxSys)
}
