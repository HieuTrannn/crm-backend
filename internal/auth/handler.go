package auth

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

const (
	accessCookie  = "access_token"
	refreshCookie = "refresh_token"
	// The access token goes to every API call; the refresh token only to the
	// auth endpoints that consume it, so it is sent as rarely as possible.
	accessCookiePath  = "/api"
	refreshCookiePath = "/api/v1/auth"
)

// Handler exposes the auth endpoints.
type Handler struct {
	svc           *Service
	tokens        *TokenIssuer
	secureCookies bool
}

// NewHandler creates the handler. secureCookies is false only outside
// production, where the API runs on plain http://localhost.
func NewHandler(svc *Service, tokens *TokenIssuer, secureCookies bool) *Handler {
	return &Handler{svc: svc, tokens: tokens, secureCookies: secureCookies}
}

// RegisterRoutes mounts the auth routes under the /api/v1 group.
func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	g := api.Group("/auth")
	g.POST("/register", h.register)
	g.POST("/login", h.login)
	g.POST("/refresh", h.refresh)
	g.POST("/logout", h.logout)
	api.GET("/me", RequireAuth(h.tokens), h.me)
}

// ErrorRules implements server.Module.
func (h *Handler) ErrorRules() []httpx.ErrorRule { return ErrorRules() }

type registerRequest struct {
	CompanyName string `json:"company_name" binding:"required,max=200"`
	Email       string `json:"email" binding:"required,email,max=254"`
	Password    string `json:"password" binding:"required,min=8"`
	FullName    string `json:"full_name" binding:"required,max=200"`
}

func (h *Handler) register(c *gin.Context) {
	var req registerRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	me, err := h.svc.Register(c.Request.Context(), RegisterInput(req))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, me)
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (h *Handler) login(c *gin.Context) {
	var req loginRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	sess, err := h.svc.Login(c.Request.Context(), req.Email, req.Password, c.ClientIP())
	if err != nil {
		_ = c.Error(err)
		return
	}
	h.setSessionCookies(c, sess)
	c.Status(http.StatusNoContent)
}

func (h *Handler) refresh(c *gin.Context) {
	cookie, err := c.Cookie(refreshCookie)
	if err != nil {
		_ = c.Error(ErrInvalidRefreshToken)
		return
	}
	sess, err := h.svc.Refresh(c.Request.Context(), cookie)
	if err != nil {
		_ = c.Error(err)
		return
	}
	h.setSessionCookies(c, sess)
	c.Status(http.StatusNoContent)
}

func (h *Handler) logout(c *gin.Context) {
	if cookie, err := c.Cookie(refreshCookie); err == nil {
		if err := h.svc.Logout(c.Request.Context(), cookie); err != nil {
			_ = c.Error(err)
			return
		}
	}
	h.clearCookies(c)
	c.Status(http.StatusNoContent)
}

func (h *Handler) me(c *gin.Context) {
	p, ok := PrincipalFrom(c.Request.Context())
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}
	me, err := h.svc.Me(c.Request.Context(), p)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, me)
}

func (h *Handler) setSessionCookies(c *gin.Context, s Session) {
	h.setCookie(c, accessCookie, s.AccessToken, accessCookiePath, s.AccessExpiresAt)
	h.setCookie(c, refreshCookie, s.RefreshCookie, refreshCookiePath, s.RefreshExpiresAt)
}

func (h *Handler) clearCookies(c *gin.Context) {
	h.setCookie(c, accessCookie, "", accessCookiePath, time.Time{})
	h.setCookie(c, refreshCookie, "", refreshCookiePath, time.Time{})
}

// setCookie writes an HttpOnly, SameSite=Lax cookie (NFR-SEC-03). A zero
// expiry deletes the cookie.
func (h *Handler) setCookie(c *gin.Context, name, value, path string, expires time.Time) {
	maxAge := -1
	if !expires.IsZero() {
		maxAge = int(time.Until(expires).Seconds())
	}
	//nolint:gosec // G124: Secure is on in production; off only for http://localhost (development/test).
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   maxAge,
		HttpOnly: true, // JavaScript (and so XSS) cannot read the tokens
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode, // not sent on cross-site POSTs: blocks CSRF
	})
}
