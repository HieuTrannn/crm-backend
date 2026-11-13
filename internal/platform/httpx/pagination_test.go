package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTrip(t *testing.T) {
	c := Cursor{CreatedAt: time.Date(2026, 11, 13, 8, 30, 15, 123456000, time.UTC), ID: uuid.Must(uuid.NewV7())}

	got, err := DecodeCursor(EncodeCursor(c))

	require.NoError(t, err)
	assert.True(t, c.CreatedAt.Equal(got.CreatedAt), "microseconds survive the round trip")
	assert.Equal(t, c.ID, got.ID)

	for _, bad := range []string{"", "!!!", "bm8tcGlwZQ", EncodeCursor(Cursor{})[:10]} {
		_, err := DecodeCursor(bad)
		assert.ErrorIs(t, err, ErrInvalidCursor, bad)
	}
}

func TestPageParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	valid := EncodeCursor(Cursor{CreatedAt: time.Now(), ID: uuid.Must(uuid.NewV7())})

	tests := []struct {
		query     string
		wantOK    bool
		wantLimit int
		firstPage bool
	}{
		{query: "", wantOK: true, wantLimit: 20, firstPage: true},
		{query: "limit=100", wantOK: true, wantLimit: 100, firstPage: true},
		{query: "limit=5&cursor=" + valid, wantOK: true, wantLimit: 5},
		{query: "limit=0"},
		{query: "limit=101"},
		{query: "limit=abc"},
		{query: "cursor=not-a-cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x?"+tt.query, nil)

			limit, after, ok := PageParams(c)

			require.Equal(t, tt.wantOK, ok)
			if !ok {
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				return
			}
			assert.Equal(t, tt.wantLimit, limit)
			assert.Equal(t, tt.firstPage, after == FirstPage())
		})
	}
}

func TestNewPage(t *testing.T) {
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	cursorOf := func(id uuid.UUID) Cursor { return Cursor{CreatedAt: time.Unix(0, 0), ID: id} }

	last := NewPage(ids, false, cursorOf)
	assert.Nil(t, last.NextCursor)

	more := NewPage(ids, true, cursorOf)
	require.NotNil(t, more.NextCursor)
	got, err := DecodeCursor(*more.NextCursor)
	require.NoError(t, err)
	assert.Equal(t, ids[1], got.ID, "next cursor points at the last item")

	empty := NewPage[uuid.UUID](nil, false, cursorOf)
	assert.NotNil(t, empty.Data, "empty page serialises as [] not null")
}
