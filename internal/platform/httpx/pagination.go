package httpx

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Page size limits for list endpoints (FR-CON-02).
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// ErrInvalidCursor is returned for a cursor this API did not produce.
var ErrInvalidCursor = errors.New("invalid cursor")

// Cursor is the keyset position of the last row of a page: the next page holds
// rows with (CreatedAt, ID) strictly smaller. Unlike OFFSET, rows inserted
// meanwhile cannot shift the page boundaries, so nothing is skipped or repeated.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// FirstPage returns a cursor beyond any real row, so the first page uses the
// same keyset query as every other page.
func FirstPage() Cursor {
	return Cursor{CreatedAt: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), ID: uuid.Max}
}

// EncodeCursor returns base64url("<RFC 3339 nano>|<uuid>"). It is opaque to clients.
func EncodeCursor(c Cursor) string {
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses a cursor produced by EncodeCursor.
func DecodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return Cursor{}, ErrInvalidCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	uid, err := uuid.Parse(id)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{CreatedAt: createdAt, ID: uid}, nil
}

// PageParams reads ?limit= (default 20, max 100) and ?cursor= (absent: first page).
// On invalid input it writes a 400 VALIDATION_ERROR and returns ok=false.
func PageParams(c *gin.Context) (limit int, after Cursor, ok bool) {
	limit = DefaultLimit
	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > MaxLimit {
			WriteErrorDetails(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid query parameters",
				map[string]string{"limit": "integer between 1 and 100"})
			return 0, Cursor{}, false
		}
		limit = n
	}
	after = FirstPage()
	if s := c.Query("cursor"); s != "" {
		cur, err := DecodeCursor(s)
		if err != nil {
			WriteErrorDetails(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid query parameters",
				map[string]string{"cursor": "invalid"})
			return 0, Cursor{}, false
		}
		after = cur
	}
	return limit, after, true
}

// Page is the list response shape: {"data": [...], "next_cursor": "..." | null}.
type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

// NewPage builds a page. hasMore tells whether rows exist after the last item;
// cursorOf extracts the keyset position of an item.
func NewPage[T any](items []T, hasMore bool, cursorOf func(T) Cursor) Page[T] {
	p := Page[T]{Data: items}
	if p.Data == nil {
		p.Data = []T{} // JSON [] rather than null
	}
	if hasMore && len(items) > 0 {
		next := EncodeCursor(cursorOf(items[len(items)-1]))
		p.NextCursor = &next
	}
	return p
}
