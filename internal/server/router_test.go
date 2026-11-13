package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

// fakePinger lets tests run without a database.
type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return NewRouter(slog.New(slog.DiscardHandler), fakePinger{})
}

func TestDocs(t *testing.T) {
	r := newTestRouter()
	MountDocs(r, "../../api/openapi.yaml")

	for path, want := range map[string]string{
		"/docs":              "SwaggerUIBundle",
		"/docs/openapi.yaml": "openapi: 3.1.0",
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, path)
		assert.Contains(t, rec.Body.String(), want, path)
	}
}

func TestHealthz(t *testing.T) {
	tests := []struct {
		name         string
		reqID        string
		wantEchoedID bool
	}{
		{name: "no request id generates one", reqID: ""},
		{name: "valid request id is echoed", reqID: "abc-123", wantEchoedID: true},
		{name: "invalid request id is replaced", reqID: "bad id\nwith newline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
			if tt.reqID != "" {
				req.Header.Set(httpx.HeaderRequestID, tt.reqID)
			}
			rec := httptest.NewRecorder()

			newTestRouter().ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
			gotID := rec.Header().Get(httpx.HeaderRequestID)
			assert.NotEmpty(t, gotID)
			if tt.wantEchoedID {
				assert.Equal(t, tt.reqID, gotID)
			} else {
				assert.NotEqual(t, tt.reqID, gotID)
			}
		})
	}
}
