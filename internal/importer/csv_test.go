package importer

import (
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadHeader(t *testing.T) {
	tests := []struct {
		name    string
		csv     string
		want    columns
		wantErr string
	}{
		{name: "all columns", csv: "full_name,email,phone,company,notes\n", want: columns{0, 1, 2, 3, 4}},
		{name: "BOM, case and spaces, other order", csv: utf8BOM + " Email ,FULL_NAME,extra\n", want: columns{1, 0, -1, -1, -1}},
		{name: "BOM on full_name itself", csv: utf8BOM + "full_name\n", want: columns{0, -1, -1, -1, -1}},
		{name: "missing full_name", csv: "email,phone\n", wantErr: "no full_name column"},
		{name: "empty file", csv: "", wantErr: "the file is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readHeader(newCSVReader(strings.NewReader(tt.csv)))
			if tt.wantErr != "" {
				var herr *headerError
				require.ErrorAs(t, err, &herr)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateRow(t *testing.T) {
	v := validator.New()
	cols := columns{0, 1, 2, 3, 4}
	tests := []struct {
		name       string
		rec        []string
		wantReason string
	}{
		{name: "valid, fields trimmed", rec: []string{"  Nguyễn Văn An ", "an@x.vn", "0901", "Acme", "note"}},
		{name: "only full_name", rec: []string{"An"}},
		{name: "empty name", rec: []string{"   ", "an@x.vn"}, wantReason: "full_name is required"},
		{name: "name of 200 runes is fine", rec: []string{strings.Repeat("Ấ", 200)}},
		{name: "name of 201 runes", rec: []string{strings.Repeat("Ấ", 201)}, wantReason: "full_name is longer than 200"},
		{name: "bad email", rec: []string{"An", "not-an-email"}, wantReason: "email is not a valid address"},
		{name: "phone too long", rec: []string{"An", "", strings.Repeat("9", 21)}, wantReason: "phone is longer than 20"},
		{name: "company too long", rec: []string{"An", "", "", strings.Repeat("c", 201)}, wantReason: "company is longer than 200"},
		{name: "notes too long", rec: []string{"An", "", "", "", strings.Repeat("n", 5001)}, wantReason: "notes is longer than 5000"},
		{name: "invalid UTF-8", rec: []string{"An\xff"}, wantReason: "invalid UTF-8"},
		{name: "NUL byte", rec: []string{"An", "", "", "", "a\x00b"}, wantReason: "NUL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row, rowErr := validateRow(v, 7, tt.rec, cols)
			if tt.wantReason != "" {
				require.NotNil(t, rowErr)
				assert.Equal(t, int32(7), rowErr.RowNumber)
				assert.Contains(t, rowErr.Reason, tt.wantReason)
				return
			}
			require.Nil(t, rowErr)
			assert.Equal(t, int32(7), row.line)
			assert.NotZero(t, row.id)
			assert.Equal(t, strings.TrimSpace(tt.rec[0]), row.fullName)
		})
	}
}

func TestCountRows(t *testing.T) {
	csv := "full_name,notes\n" +
		"An,\"two\nlines\"\n" + // one record over two lines
		"Bình,\"bad \"quote\"\n" + // malformed, still counted
		"Chi,x\n"
	n, err := countRows(strings.NewReader(csv))
	require.NoError(t, err)
	assert.Equal(t, int32(3), n)

	n, err = countRows(strings.NewReader(""))
	require.NoError(t, err)
	assert.Zero(t, n)
}
