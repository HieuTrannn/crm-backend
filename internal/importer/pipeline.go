package importer

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"sync"

	"github.com/go-playground/validator/v10"
	"golang.org/x/sync/errgroup"
)

// batchSize is the number of lines (valid or not) stored per transaction.
const batchSize = 500

// rawRow is one CSV record as read, or the parse error for that line.
type rawRow struct {
	line     int32
	rec      []string
	parseErr error
}

// result is the outcome of validating one line: a row to insert or an error.
type result struct {
	row contactRow
	err *RowError
}

// flushFunc stores one batch (rows, errors and progress) in one transaction.
type flushFunc func(ctx context.Context, b batch) error

// runPipeline streams src through
//
//	reader --rows--> N validators --results--> writer --flush--> database
//
// Every stage stops when ctx is cancelled or another stage fails (errgroup
// cancels the shared context), so no goroutine is left blocked on a channel.
// Each channel is closed by the only side that sends on it, once it has stopped.
func runPipeline(ctx context.Context, src io.Reader, workers int, v *validator.Validate, flush flushFunc) error {
	cr := newCSVReader(src)
	cols, err := readHeader(cr)
	if err != nil {
		return err
	}

	g, ctx := errgroup.WithContext(ctx)
	rows := make(chan rawRow, batchSize)
	results := make(chan result, batchSize)

	// Stage 1: the single reader owns rows and closes it when it returns.
	g.Go(func() error {
		defer close(rows)
		return readRows(ctx, cr, rows)
	})

	// Stage 2: N validators share rows. results has N senders, so none of them
	// may close it; a separate goroutine closes it after all of them returned.
	var validators sync.WaitGroup
	for range workers {
		validators.Add(1)
		g.Go(func() error {
			defer validators.Done()
			return validateRows(ctx, v, cols, rows, results)
		})
	}
	g.Go(func() error {
		validators.Wait()
		close(results)
		return nil
	})

	// Stage 3: the single writer drains results until it is closed.
	g.Go(func() error {
		return writeBatches(ctx, results, flush)
	})

	return g.Wait()
}

func readRows(ctx context.Context, cr *csv.Reader, rows chan<- rawRow) error {
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		row := rawRow{rec: rec}
		var perr *csv.ParseError
		switch {
		case errors.As(err, &perr):
			row.line, row.parseErr = int32(perr.StartLine), err //nolint:gosec // a 20 MB file has far fewer than 2^31 lines
		case err != nil:
			return err
		default:
			line, _ := cr.FieldPos(0)
			row.line = int32(line) //nolint:gosec // see above
		}
		select {
		case rows <- row:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func validateRows(ctx context.Context, v *validator.Validate, cols columns, rows <-chan rawRow, results chan<- result) error {
	for raw := range rows {
		var res result
		if raw.parseErr != nil {
			res.err = &RowError{RowNumber: raw.line, Reason: "malformed CSV: " + raw.parseErr.Error()}
		} else {
			res.row, res.err = validateRow(v, raw.line, raw.rec, cols)
		}
		select {
		case results <- res:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func writeBatches(ctx context.Context, results <-chan result, flush flushFunc) error {
	var b batch
	for res := range results {
		if res.err != nil {
			b.errs = append(b.errs, *res.err)
		} else {
			b.rows = append(b.rows, res.row)
		}
		if len(b.rows)+len(b.errs) >= batchSize {
			if err := flush(ctx, b); err != nil {
				return err
			}
			b = batch{}
		}
	}
	// results is also closed when the validators stop because of a cancel:
	// do not store a partial last batch in that case.
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(b.rows)+len(b.errs) > 0 {
		return flush(ctx, b)
	}
	return nil
}
