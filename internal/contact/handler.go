package contact

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/HieuTrannn/crm-backend/internal/auth"
	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

// Handler exposes the contact endpoints; every route requires authentication.
type Handler struct {
	svc         *Service
	requireAuth gin.HandlerFunc
}

// NewHandler creates the handler. requireAuth is auth.RequireAuth.
func NewHandler(svc *Service, requireAuth gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, requireAuth: requireAuth}
}

// RegisterRoutes mounts the contact routes under the /api/v1 group.
func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	g := api.Group("/contacts", h.requireAuth)
	g.GET("", h.list)
	g.POST("", h.create)
	g.GET("/:id", h.get)
	g.PATCH("/:id", h.update)
	g.DELETE("/:id", h.delete)
}

// ErrorRules implements server.Module.
func (h *Handler) ErrorRules() []httpx.ErrorRule { return ErrorRules() }

type createRequest struct {
	FullName string     `json:"full_name" binding:"required,min=1,max=200"`
	Email    *string    `json:"email" binding:"omitempty,email,max=254"`
	Phone    *string    `json:"phone" binding:"omitempty,max=20"`
	Company  *string    `json:"company" binding:"omitempty,max=200"`
	Notes    *string    `json:"notes" binding:"omitempty,max=5000"`
	OwnerID  *uuid.UUID `json:"owner_id"`
}

// updateRequest: absent fields are unchanged; "" clears an optional text field.
// omitnil (not omitempty) on full_name so an explicit "" is rejected.
type updateRequest struct {
	FullName *string    `json:"full_name" binding:"omitnil,min=1,max=200"`
	Email    *string    `json:"email" binding:"omitempty,email,max=254"`
	Phone    *string    `json:"phone" binding:"omitempty,max=20"`
	Company  *string    `json:"company" binding:"omitempty,max=200"`
	Notes    *string    `json:"notes" binding:"omitempty,max=5000"`
	OwnerID  *uuid.UUID `json:"owner_id"`
}

func (h *Handler) create(c *gin.Context) {
	p, ok := auth.CurrentPrincipal(c)
	if !ok {
		return
	}
	var req createRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	contact, err := h.svc.Create(c.Request.Context(), p.TenantID, p.UserID, CreateInput(req))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, contact)
}

func (h *Handler) get(c *gin.Context) {
	p, id, ok := principalAndID(c)
	if !ok {
		return
	}
	contact, err := h.svc.Get(c.Request.Context(), p.TenantID, id)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, contact)
}

func (h *Handler) update(c *gin.Context) {
	p, id, ok := principalAndID(c)
	if !ok {
		return
	}
	var req updateRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	contact, err := h.svc.Update(c.Request.Context(), p.TenantID, id, UpdateInput(req))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, contact)
}

func (h *Handler) delete(c *gin.Context) {
	p, id, ok := principalAndID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), p.TenantID, id); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) list(c *gin.Context) {
	p, ok := auth.CurrentPrincipal(c)
	if !ok {
		return
	}
	limit, after, ok := httpx.PageParams(c)
	if !ok {
		return
	}
	items, hasMore, err := h.svc.List(c.Request.Context(), p.TenantID, ListParams{
		Query: c.Query("q"), Limit: limit, AfterCreatedAt: after.CreatedAt, AfterID: after.ID,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewPage(items, hasMore, func(ct Contact) httpx.Cursor {
		return httpx.Cursor{CreatedAt: ct.CreatedAt, ID: ct.ID}
	}))
}

// principalAndID also parses :id. A malformed id is reported as 404, like any
// id that does not exist in the caller's tenant.
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
