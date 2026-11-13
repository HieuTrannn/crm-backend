package httpx

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// BindJSON decodes and validates the body into dst. On failure it writes a 400
// VALIDATION_ERROR (with the failing fields and rules as details) and returns false.
func BindJSON(c *gin.Context, dst any) bool {
	err := c.ShouldBindJSON(dst)
	if err == nil {
		return true
	}
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		details := make(map[string]string, len(verrs))
		for _, fe := range verrs {
			details[fe.Field()] = fe.Tag()
		}
		WriteErrorDetails(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body", details)
		return false
	}
	WriteError(c, http.StatusBadRequest, "VALIDATION_ERROR", "malformed JSON body")
	return false
}
