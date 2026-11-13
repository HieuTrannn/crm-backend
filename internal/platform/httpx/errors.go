// Package httpx holds shared HTTP helpers: error format and middleware.
package httpx

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ErrorBody is the "error" object of the standard error response (FR-SYS-02).
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// ErrorResponse is the standard error envelope returned by every endpoint.
type ErrorResponse struct {
	Error     ErrorBody `json:"error"`
	RequestID string    `json:"request_id"`
}

// WriteError aborts the request with the standard error JSON.
func WriteError(c *gin.Context, status int, code, message string) {
	WriteErrorDetails(c, status, code, message, nil)
}

// WriteErrorDetails is WriteError with a details payload (e.g. invalid fields).
func WriteErrorDetails(c *gin.Context, status int, code, message string, details any) {
	c.AbortWithStatusJSON(status, ErrorResponse{
		Error:     ErrorBody{Code: code, Message: message, Details: details},
		RequestID: RequestIDFrom(c),
	})
}

// ErrorRule maps a sentinel error (matched with errors.Is) to an HTTP response.
type ErrorRule struct {
	Err     error
	Status  int
	Code    string
	Message string
}

// Detailer is implemented by errors that carry data for the response's
// "details" field, e.g. the latest version of a record on VERSION_CONFLICT.
type Detailer interface {
	ErrorDetails() any
}

// Errors turns the last error a handler attached with c.Error into the
// standard error JSON. Unknown errors become a logged 500 that reveals nothing.
func Errors(log *slog.Logger, rules ...ErrorRule) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}
		err := c.Errors.Last().Err
		for _, r := range rules {
			if errors.Is(err, r.Err) {
				var details any
				var d Detailer
				if errors.As(err, &d) {
					details = d.ErrorDetails()
				}
				WriteErrorDetails(c, r.Status, r.Code, r.Message, details)
				return
			}
		}
		log.ErrorContext(c.Request.Context(), "unhandled error",
			slog.String("request_id", RequestIDFrom(c)),
			slog.Any("error", err),
		)
		WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}
