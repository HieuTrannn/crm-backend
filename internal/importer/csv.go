package importer

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

// Field limits mirror the CHECK constraints of the contacts table, so a row that
// passes validation cannot make its whole batch fail in the database.
const (
	maxFullName = 200
	maxEmail    = 254
	maxPhone    = 20
	maxCompany  = 200
	maxNotes    = 5000
)

// utf8BOM is U+FEFF, which some editors put at the start of a UTF-8 file.
const utf8BOM = string(rune(0xFEFF))

// headerError is a problem with the CSV header; its message is shown to the user.
type headerError struct{ msg string }

func (e *headerError) Error() string { return e.msg }

// columns holds the position of each known column, -1 when absent.
type columns struct {
	fullName, email, phone, company, notes int
}

// newCSVReader reads records of any width; columns are matched by header name.
func newCSVReader(r io.Reader) *csv.Reader {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	return cr
}

// readHeader reads the first record and maps column names (case-insensitive,
// trimmed, UTF-8 BOM removed). Unknown columns are ignored; full_name is required.
func readHeader(cr *csv.Reader) (columns, error) {
	rec, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return columns{}, &headerError{"the file is empty"}
	}
	if err != nil {
		return columns{}, &headerError{fmt.Sprintf("cannot read the header: %v", err)}
	}
	cols := columns{-1, -1, -1, -1, -1}
	for i, name := range rec {
		if i == 0 {
			name = strings.TrimPrefix(name, utf8BOM) // Excel writes a BOM before the first column
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "full_name":
			cols.fullName = i
		case "email":
			cols.email = i
		case "phone":
			cols.phone = i
		case "company":
			cols.company = i
		case "notes":
			cols.notes = i
		}
	}
	if cols.fullName < 0 {
		return columns{}, &headerError{"the header has no full_name column"}
	}
	return cols, nil
}

// field returns the trimmed value at position i, or "" when the column is
// absent or the record is shorter.
func field(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

// validateRow turns one CSV record into a contact row or a row error, applying
// the same rules as the contacts API (FR-CON-01).
func validateRow(v *validator.Validate, line int32, rec []string, cols columns) (contactRow, *RowError) {
	row := contactRow{
		line:     line,
		fullName: field(rec, cols.fullName),
		email:    field(rec, cols.email),
		phone:    field(rec, cols.phone),
		company:  field(rec, cols.company),
		notes:    field(rec, cols.notes),
	}
	fail := func(reason string) (contactRow, *RowError) {
		return contactRow{}, &RowError{RowNumber: line, Reason: reason}
	}

	for _, s := range []string{row.fullName, row.email, row.phone, row.company, row.notes} {
		// PostgreSQL rejects both in text columns, which would fail the whole batch.
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return fail("contains invalid UTF-8 or a NUL character")
		}
	}
	switch {
	case row.fullName == "":
		return fail("full_name is required")
	case utf8.RuneCountInString(row.fullName) > maxFullName:
		return fail(fmt.Sprintf("full_name is longer than %d characters", maxFullName))
	case utf8.RuneCountInString(row.email) > maxEmail:
		return fail(fmt.Sprintf("email is longer than %d characters", maxEmail))
	case row.email != "" && v.Var(row.email, "email") != nil:
		return fail("email is not a valid address")
	case utf8.RuneCountInString(row.phone) > maxPhone:
		return fail(fmt.Sprintf("phone is longer than %d characters", maxPhone))
	case utf8.RuneCountInString(row.company) > maxCompany:
		return fail(fmt.Sprintf("company is longer than %d characters", maxCompany))
	case utf8.RuneCountInString(row.notes) > maxNotes:
		return fail(fmt.Sprintf("notes is longer than %d characters", maxNotes))
	}

	id, err := uuid.NewV7()
	if err != nil {
		return fail("internal error generating an id")
	}
	row.id = id
	return row, nil
}

// countRows counts data records (header excluded), including malformed ones,
// so total_rows matches what the pipeline will process. It streams the file.
func countRows(r io.Reader) (int32, error) {
	cr := newCSVReader(r)
	var n int32
	for {
		_, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		var perr *csv.ParseError
		if err != nil && !errors.As(err, &perr) {
			return 0, err
		}
		n++
	}
	return max(n-1, 0), nil
}
