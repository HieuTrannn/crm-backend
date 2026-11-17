// Package dashboard reports pipeline and monthly sales figures (FR-DASH-01, 02).
// All arithmetic happens in SQL; this package only moves rows to JSON.
package dashboard

// StageSummary is the number and total amount (VND) of live deals in one stage.
type StageSummary struct {
	Stage       string `json:"stage"`
	DealCount   int64  `json:"deal_count"`
	TotalAmount int64  `json:"total_amount"`
}

// MonthSummary covers deals closed (won or lost) in one calendar month,
// Asia/Ho_Chi_Minh time. WinRate is won / (won + lost), 0 when nothing closed.
type MonthSummary struct {
	Month     string  `json:"month"` // "YYYY-MM"
	WonCount  int64   `json:"won_count"`
	LostCount int64   `json:"lost_count"`
	WonAmount int64   `json:"won_amount"`
	WinRate   float64 `json:"win_rate"`
}
