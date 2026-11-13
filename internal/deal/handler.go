package deal

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/HieuTrannn/crm-backend/internal/auth"
	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

// Handler exposes the deal endpoints; every route requires authentication.
type Handler struct {
	svc         *Service
	requireAuth gin.HandlerFunc
}

// NewHandler creates the handler. requireAuth is auth.RequireAuth.
func NewHandler(svc *Service, requireAuth gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, requireAuth: requireAuth}
}

// RegisterRoutes mounts the deal routes under the /api/v1 group.
func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	g := api.Group("/deals", h.requireAuth)
	g.GET("", h.list)
	g.POST("", h.create)
	g.GET("/:id", h.get)
	g.PATCH("/:id", h.update)
	g.DELETE("/:id", h.delete)
	g.POST("/:id/transition", h.transition)
	g.GET("/:id/history", h.history)
}

// ErrorRules implements server.Module.
func (h *Handler) ErrorRules() []httpx.ErrorRule { return ErrorRules() }

type createRequest struct {
	ContactID         uuid.UUID  `json:"contact_id" binding:"required"`
	Title             string     `json:"title" binding:"required,min=1,max=200"`
	Amount            *int64     `json:"amount" binding:"required,min=0"` // pointer so 0 is allowed
	ExpectedCloseDate *Date      `json:"expected_close_date"`
	OwnerID           *uuid.UUID `json:"owner_id"`
}

type updateRequest struct {
	Version           *int32     `json:"version" binding:"required,min=1"`
	Title             *string    `json:"title" binding:"omitnil,min=1,max=200"`
	Amount            *int64     `json:"amount" binding:"omitnil,min=0"`
	ExpectedCloseDate *Date      `json:"expected_close_date"`
	OwnerID           *uuid.UUID `json:"owner_id"`
}

type transitionRequest struct {
	ToStage      Stage  `json:"to_stage" binding:"required,oneof=lead qualified proposal won lost"`
	Version      *int32 `json:"version" binding:"required,min=1"`
	LostReason   string `json:"lost_reason" binding:"max=1000"`
	ReopenReason string `json:"reopen_reason" binding:"max=1000"`
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
	d, err := h.svc.Create(c.Request.Context(), p.TenantID, p.UserID, CreateInput{
		ContactID: req.ContactID, Title: req.Title, Amount: *req.Amount,
		ExpectedCloseDate: req.ExpectedCloseDate, OwnerID: req.OwnerID,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, d)
}

func (h *Handler) get(c *gin.Context) {
	p, id, ok := principalAndID(c)
	if !ok {
		return
	}
	d, err := h.svc.Get(c.Request.Context(), p.TenantID, id)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, d)
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
	d, err := h.svc.Update(c.Request.Context(), p.TenantID, id, UpdateInput{
		Version: *req.Version, Title: req.Title, Amount: req.Amount,
		ExpectedCloseDate: req.ExpectedCloseDate, OwnerID: req.OwnerID,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, d)
}

func (h *Handler) transition(c *gin.Context) {
	p, id, ok := principalAndID(c)
	if !ok {
		return
	}
	var req transitionRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	d, err := h.svc.Transition(c.Request.Context(), p.TenantID, p.UserID, id, TransitionInput{
		To: req.ToStage, Version: *req.Version, LostReason: req.LostReason, ReopenReason: req.ReopenReason,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, d)
}

// delete takes the version as ?version=N: DELETE requests carry no body.
func (h *Handler) delete(c *gin.Context) {
	p, id, ok := principalAndID(c)
	if !ok {
		return
	}
	version, err := strconv.ParseInt(c.Query("version"), 10, 32)
	if err != nil || version < 1 {
		httpx.WriteErrorDetails(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid query parameters",
			map[string]string{"version": "required, integer >= 1"})
		return
	}
	if err := h.svc.Delete(c.Request.Context(), p.TenantID, id, int32(version)); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// history is not paginated: a deal gains one entry per stage change, so the
// list stays small.
func (h *Handler) history(c *gin.Context) {
	p, id, ok := principalAndID(c)
	if !ok {
		return
	}
	entries, err := h.svc.History(c.Request.Context(), p.TenantID, id)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if entries == nil {
		entries = []HistoryEntry{} // JSON [] rather than null
	}
	c.JSON(http.StatusOK, gin.H{"data": entries})
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
	params := ListParams{Limit: limit, AfterCreatedAt: after.CreatedAt, AfterID: after.ID}
	invalid := map[string]string{}
	if s := c.Query("stage"); s != "" {
		stage := Stage(s)
		if !stage.Valid() {
			invalid["stage"] = "one of lead, qualified, proposal, won, lost"
		}
		params.Stage = &stage
	}
	params.OwnerID = uuidQuery(c, "owner_id", invalid)
	params.ContactID = uuidQuery(c, "contact_id", invalid)
	if len(invalid) > 0 {
		httpx.WriteErrorDetails(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid query parameters", invalid)
		return
	}

	items, hasMore, err := h.svc.List(c.Request.Context(), p.TenantID, params)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewPage(items, hasMore, func(d Deal) httpx.Cursor {
		return httpx.Cursor{CreatedAt: d.CreatedAt, ID: d.ID}
	}))
}

// uuidQuery parses an optional UUID query parameter, recording a bad value in invalid.
func uuidQuery(c *gin.Context, name string, invalid map[string]string) *uuid.UUID {
	s := c.Query(name)
	if s == "" {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		invalid[name] = "uuid"
		return nil
	}
	return &id
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
