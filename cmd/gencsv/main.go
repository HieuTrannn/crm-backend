// Command gencsv writes a contacts CSV for import tests and benchmarks.
//
//	go run ./cmd/gencsv -n 100000 -o contacts.csv
//
// Every 20th row (5%) is invalid on purpose; see importer.GenerateCSV.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"

	"github.com/HieuTrannn/crm-backend/internal/importer"
)

func main() {
	n := flag.Int("n", 100_000, "number of data rows")
	out := flag.String("o", "contacts.csv", "output file")
	flag.Parse()

	if err := run(*n, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gencsv:", err)
		os.Exit(1)
	}
}

func run(n int, path string) error {
	f, err := os.Create(path) //nolint:gosec // path comes from the developer's own flag
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	invalid, err := importer.GenerateCSV(w, n)
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d rows, %d invalid on purpose\n", path, n, invalid)
	return nil
}
