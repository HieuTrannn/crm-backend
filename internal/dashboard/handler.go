package dashboard

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/HieuTrannn/crm-backend/internal/auth"
	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

// Handler exposes the dashboard endpoints; every route requires authentication.
type Handler struct {
	svc         *Service
	requireAuth gin.HandlerFunc
}

// NewHandler creates the handler. requireAuth is auth.RequireAuth.
func NewHandler(svc *Service, requireAuth gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, requireAuth: requireAuth}
}

// RegisterRoutes mounts the dashboard routes under the /api/v1 group.
func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	g := api.Group("/dashboard", h.requireAuth)
	g.GET("/pipeline", h.pipeline)
	g.GET("/monthly", h.monthly)
}

// ErrorRules implements server.Module. The dashboard has no business errors.
func (h *Handler) ErrorRules() []httpx.ErrorRule { return nil }

func (h *Handler) pipeline(c *gin.Context) {
	p, ok := auth.CurrentPrincipal(c)
	if !ok {
		return
	}
	stages, err := h.svc.Pipeline(c.Request.Context(), p.TenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": stages})
}

func (h *Handler) monthly(c *gin.Context) {
	p, ok := auth.CurrentPrincipal(c)
	if !ok {
		return
	}
	months, err := h.svc.Monthly(c.Request.Context(), p.TenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": months})
}
