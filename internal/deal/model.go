package deal

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Deal is a sales opportunity with a contact. Amount is whole VND (never float).
type Deal struct {
	ID                uuid.UUID  `json:"id"`
	ContactID         uuid.UUID  `json:"contact_id"`
	Title             string     `json:"title"`
	Amount            int64      `json:"amount"`
	Stage             Stage      `json:"stage"`
	ExpectedCloseDate *Date      `json:"expected_close_date"`
	OwnerID           uuid.UUID  `json:"owner_id"`
	LostReason        *string    `json:"lost_reason"`
	Version           int32      `json:"version"`
	ClosedAt          *time.Time `json:"closed_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// Date is a calendar day, serialized as "2006-01-02" (no time, no zone).
type Date struct {
	time.Time
}

const dateLayout = time.DateOnly

// MarshalJSON writes "YYYY-MM-DD".
func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Format(dateLayout))
}

// UnmarshalJSON reads "YYYY-MM-DD".
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("date must be a string: %w", err)
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return fmt.Errorf("date must be YYYY-MM-DD: %w", err)
	}
	d.Time = t
	return nil
}

// CreateInput holds a new deal; it always starts in StageLead.
// A nil OwnerID means "the caller".
type CreateInput struct {
	ContactID         uuid.UUID
	Title             string
	Amount            int64
	ExpectedCloseDate *Date
	OwnerID           *uuid.UUID
}

// UpdateInput is a partial update guarded by the version the client last read.
type UpdateInput struct {
	Version           int32
	Title             *string
	Amount            *int64
	ExpectedCloseDate *Date
	OwnerID           *uuid.UUID
}

// TransitionInput moves a deal to another stage (FR-DEAL-02).
type TransitionInput struct {
	To           Stage
	Version      int32
	LostReason   string // required when To is lost
	ReopenReason string // required when reopening a lost deal
}

// ListParams selects one page; nil filters are not applied (FR-DEAL-05).
type ListParams struct {
	Stage          *Stage
	OwnerID        *uuid.UUID
	ContactID      *uuid.UUID
	Limit          int
	AfterCreatedAt time.Time
	AfterID        uuid.UUID
}

// HistoryEntry is one recorded stage change (FR-DEAL-03). FromStage is nil for
// the entry written when the deal was created.
type HistoryEntry struct {
	ID        uuid.UUID `json:"id"`
	FromStage *Stage    `json:"from_stage"`
	ToStage   Stage     `json:"to_stage"`
	Reason    *string   `json:"reason"`
	ChangedBy uuid.UUID `json:"changed_by"`
	ChangedAt time.Time `json:"changed_at"`
}

// stageChange is one row of deal_stage_history. From is nil when the deal is created.
type stageChange struct {
	DealID    uuid.UUID
	From      *Stage
	To        Stage
	Reason    *string
	ChangedBy uuid.UUID
}
