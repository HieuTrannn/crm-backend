package importer

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// GenerateCSV writes a contacts CSV with a header and n data rows, for tests and
// benchmarks (cmd/gencsv). It is deterministic: every 20th row (5%) is invalid,
// cycling through an empty name, a malformed email, a too-long phone and an
// email duplicating the previous row. It returns the number of invalid rows.
func GenerateCSV(w io.Writer, n int) (int, error) {
	families := []string{"Nguyễn", "Trần", "Lê", "Phạm", "Hoàng", "Huỳnh", "Phan", "Vũ", "Võ", "Đặng"}
	middles := []string{"Văn", "Thị", "Minh", "Ngọc", "Thanh", "Quốc", "Gia", "Đức"}
	givens := []string{"An", "Bình", "Chi", "Dũng", "Giang", "Hà", "Hải", "Hoa", "Khoa", "Lan", "Long", "Mai", "Nam", "Thảo"}

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"full_name", "email", "phone", "company", "notes"}); err != nil {
		return 0, err
	}
	invalid := 0
	for i := range n {
		name := families[i%len(families)] + " " + middles[i%len(middles)] + " " + givens[i%len(givens)]
		email := fmt.Sprintf("kh%d@example.vn", i)
		phone := fmt.Sprintf("09%08d", i)
		company := fmt.Sprintf("Công ty %d", i%500)
		notes := ""
		if i%7 == 0 {
			notes = "Khách quen, gọi buổi sáng"
		}
		if i%20 == 19 {
			invalid++
			switch (i / 20) % 4 {
			case 0:
				name = ""
			case 1:
				email = "not-an-email"
			case 2:
				phone = strings.Repeat("9", 21)
			case 3:
				email = fmt.Sprintf("kh%d@example.vn", i-1) // already used by the previous row
			}
		}
		if err := cw.Write([]string{name, email, phone, company, notes}); err != nil {
			return 0, err
		}
	}
	cw.Flush()
	return invalid, cw.Error()
}
