package importer

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/HieuTrannn/crm-backend/internal/auth"
	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

// multipartOverhead allows for the multipart boundaries and headers around the file.
const multipartOverhead = 1 << 20

// Handler exposes the import endpoints; every route requires authentication.
type Handler struct {
	svc         *Service
	requireAuth gin.HandlerFunc
}

// NewHandler creates the handler. requireAuth is auth.RequireAuth.
func NewHandler(svc *Service, requireAuth gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, requireAuth: requireAuth}
}

// RegisterRoutes mounts the import routes under the /api/v1 group.
func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	g := api.Group("/imports", h.requireAuth)
	g.POST("/contacts", h.upload)
	g.GET("/:id", h.get)
	g.GET("/:id/errors", h.errors)
	g.POST("/:id/cancel", h.cancel)
}

// ErrorRules implements server.Module.
func (h *Handler) ErrorRules() []httpx.ErrorRule { return ErrorRules() }

// upload streams the "file" part of a multipart/form-data body to the service
// without buffering it in memory, then answers 202 with the new job.
func (h *Handler) upload(c *gin.Context) {
	p, ok := auth.CurrentPrincipal(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxFileSize+multipartOverhead)
	mr, err := c.Request.MultipartReader()
	if err != nil {
		missingFile(c)
		return
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			missingFile(c)
			return
		}
		if err != nil {
			uploadError(c, err)
			return
		}
		if part.FormName() != "file" {
			continue
		}
		job, err := h.svc.Start(c.Request.Context(), p.TenantID, p.UserID, part)
		if err != nil {
			uploadError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, job)
		return
	}
}

func (h *Handler) get(c *gin.Context) {
	p, id, ok := principalAndID(c)
	if !ok {
		return
	}
	job, err := h.svc.Get(c.Request.Context(), p.TenantID, id)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, job)
}

// errors lists rejected lines by line number. The cursor is the last line
// number of the previous page.
func (h *Handler) errors(c *gin.Context) {
	p, id, ok := principalAndID(c)
	if !ok {
		return
	}
	limit, ok := httpx.LimitParam(c)
	if !ok {
		return
	}
	var after int32
	if s := c.Query("cursor"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil || n < 0 {
			httpx.WriteErrorDetails(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid query parameters",
				map[string]string{"cursor": "invalid"})
			return
		}
		after = int32(n)
	}
	rows, hasMore, err := h.svc.Errors(c.Request.Context(), p.TenantID, id, after, limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	page := httpx.Page[RowError]{Data: rows}
	if page.Data == nil {
		page.Data = []RowError{}
	}
	if hasMore {
		next := strconv.Itoa(int(rows[len(rows)-1].RowNumber))
		page.NextCursor = &next
	}
	c.JSON(http.StatusOK, page)
}

func (h *Handler) cancel(c *gin.Context) {
	p, id, ok := principalAndID(c)
	if !ok {
		return
	}
	if err := h.svc.Cancel(c.Request.Context(), p.TenantID, id); err != nil {
		_ = c.Error(err)
		return
	}
	// Accepted: the job stops shortly and its status becomes "cancelled".
	c.Status(http.StatusAccepted)
}

func missingFile(c *gin.Context) {
	httpx.WriteErrorDetails(c, http.StatusBadRequest, "VALIDATION_ERROR", "a multipart/form-data body is required",
		map[string]string{"file": "required"})
}

// uploadError maps a body larger than the limit to 413 and other errors to the
// error middleware.
func uploadError(c *gin.Context, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		err = ErrFileTooLarge
	}
	_ = c.Error(err)
}

// principalAndID also parses :id. A malformed id is reported as 404.
func principalAndID(c *gin.Context) (auth.Principal, uuid.UUID, bool) {
	p, ok := auth.CurrentPrincipal(c)
	if !ok {
		return auth.Principal{}, uuid.Nil, false
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		_ = c.Error(ErrNotFound)
		return auth.Principal{}, uuid.Nil, false
	}
	return p, id, true
}
