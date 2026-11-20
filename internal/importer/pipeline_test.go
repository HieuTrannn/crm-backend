package importer

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// generated returns a CSV of n rows and the line numbers the pipeline itself
// must reject (duplicate emails are only caught by the database).
func generated(t *testing.T, n int) ([]byte, map[int32]bool) {
	t.Helper()
	var buf bytes.Buffer
	_, err := GenerateCSV(&buf, n)
	require.NoError(t, err)
	bad := map[int32]bool{}
	for i := range n {
		if i%20 == 19 && (i/20)%4 != 3 {
			bad[int32(i+2)] = true //nolint:gosec // test sizes are small; +2: header is line 1
		}
	}
	return buf.Bytes(), bad
}

func TestPipeline_EveryLineOnceInBatches(t *testing.T) {
	defer goleak.VerifyNone(t)
	data, bad := generated(t, 1234)

	seen := map[int32]int{}
	var errLines []int32
	flushes := 0
	err := runPipeline(t.Context(), bytes.NewReader(data), 4, validator.New(), func(_ context.Context, b batch) error {
		flushes++
		assert.LessOrEqual(t, len(b.rows)+len(b.errs), batchSize)
		for _, r := range b.rows {
			seen[r.line]++
		}
		for _, e := range b.errs {
			seen[e.RowNumber]++
			errLines = append(errLines, e.RowNumber)
		}
		return nil
	})

	require.NoError(t, err)
	assert.Len(t, seen, 1234, "every data line reaches the writer")
	for line, n := range seen {
		require.Equal(t, 1, n, "line %d handled once", line)
		require.True(t, line >= 2 && line <= 1235, "line numbers are file lines")
	}
	assert.Len(t, errLines, len(bad))
	for _, l := range errLines {
		assert.True(t, bad[l], "line %d rejected on purpose", l)
	}
	assert.Equal(t, 3, flushes, "1234 lines in batches of 500")
}

func TestPipeline_CancelMidwayLeaksNoGoroutine(t *testing.T) {
	defer goleak.VerifyNone(t)
	data, _ := generated(t, 50_000)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	flushes := 0
	err := runPipeline(ctx, bytes.NewReader(data), 8, validator.New(), func(context.Context, batch) error {
		flushes++
		if flushes == 2 {
			cancel() // like POST /imports/{id}/cancel while the import runs
		}
		return nil
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, flushes, 100, "stopped long before the 100 batches of the file")
	// goleak.VerifyNone (deferred) fails the test if the reader, a validator,
	// the closer or the writer is still running.
}

func TestPipeline_FlushErrorStopsAllStages(t *testing.T) {
	defer goleak.VerifyNone(t)
	data, _ := generated(t, 50_000)
	boom := errors.New("database down")

	flushes := 0
	err := runPipeline(t.Context(), bytes.NewReader(data), 8, validator.New(), func(context.Context, batch) error {
		flushes++
		if flushes == 3 {
			return boom
		}
		return nil
	})

	require.ErrorIs(t, err, boom)
	assert.Equal(t, 3, flushes, "nothing is written after the failure")
}

func TestPipeline_HeaderErrorStartsNothing(t *testing.T) {
	defer goleak.VerifyNone(t)
	err := runPipeline(t.Context(), strings.NewReader("email\nan@x.vn\n"), 4, validator.New(),
		func(context.Context, batch) error {
			t.Fatal("flush must not be called")
			return nil
		})
	var herr *headerError
	require.ErrorAs(t, err, &herr)
}
