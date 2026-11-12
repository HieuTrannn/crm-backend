// Package httpx holds shared HTTP helpers: error format and middleware.
package httpx

import "github.com/gin-gonic/gin"

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
	c.AbortWithStatusJSON(status, ErrorResponse{
		Error:     ErrorBody{Code: code, Message: message},
		RequestID: RequestIDFrom(c),
	})
}
