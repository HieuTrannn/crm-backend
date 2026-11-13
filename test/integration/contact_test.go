//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HieuTrannn/crm-backend/internal/auth"
	"github.com/HieuTrannn/crm-backend/internal/contact"
	"github.com/HieuTrannn/crm-backend/internal/server"
)

// alwaysOpenDeals stands in for the future deal module: every contact has an open deal.
type alwaysOpenDeals struct{}

func (alwaysOpenDeals) HasOpenDeals(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}

type contactEnv struct {
	h       http.Handler
	s       seeded
	cookieA *http.Cookie
	cookieB *http.Cookie
	baseEnv env
}

func newContactEnv(t *testing.T, deals contact.DealChecker) contactEnv {
	t.Helper()
	e := setup(t)
	s := seed(t, e)
	tokens := auth.NewTokenIssuer(testJWTSecret)
	h := server.NewRouter(slog.New(slog.DiscardHandler), e.app,
		contact.NewHandler(contact.NewService(contact.NewPostgresRepository(e.db), deals), auth.RequireAuth(tokens)))
	cookie := func(user, tenant uuid.UUID) *http.Cookie {
		tok, _, err := tokens.Issue(auth.Principal{UserID: user, TenantID: tenant, Role: auth.RoleOwner}, time.Now())
		require.NoError(t, err)
		return &http.Cookie{Name: "access_token", Value: tok}
	}
	return contactEnv{h: h, s: s, cookieA: cookie(s.userA, s.tenantA), cookieB: cookie(s.userB, s.tenantB), baseEnv: e}
}

func createContact(t *testing.T, ce contactEnv, cookie *http.Cookie, body map[string]any) contact.Contact {
	t.Helper()
	rec := call(t, ce.h, http.MethodPost, "/api/v1/contacts", body, cookie)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var c contact.Contact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &c))
	return c
}

type contactPage struct {
	Data       []contact.Contact `json:"data"`
	NextCursor *string           `json:"next_cursor"`
}

func listContacts(t *testing.T, ce contactEnv, cookie *http.Cookie, query url.Values) contactPage {
	t.Helper()
	rec := call(t, ce.h, http.MethodGet, "/api/v1/contacts?"+query.Encode(), nil, cookie)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var p contactPage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p
}

// TestContactPagination proves FR-CON-02: walking the pages returns every
// contact exactly once, even when new contacts are inserted between two pages.
func TestContactPagination(t *testing.T) {
	ce := newContactEnv(t, contact.NoDeals{})

	original := map[uuid.UUID]bool{}
	for i := range 25 {
		original[createContact(t, ce, ce.cookieA, map[string]any{"full_name": fmt.Sprintf("Contact %02d", i)}).ID] = true
	}

	seen := map[uuid.UUID]int{}
	var cursor string
	var prev time.Time
	for page := 0; ; page++ {
		q := url.Values{"limit": {"10"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		p := listContacts(t, ce, ce.cookieA, q)
		for _, c := range p.Data {
			seen[c.ID]++
			if !prev.IsZero() {
				assert.False(t, c.CreatedAt.After(prev), "newest first")
			}
			prev = c.CreatedAt
		}
		if page == 0 {
			// Newer rows inserted between page 1 and page 2 must not shift later pages.
			for i := range 5 {
				createContact(t, ce, ce.cookieA, map[string]any{"full_name": fmt.Sprintf("Late %d", i)})
			}
		}
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}

	assert.Len(t, seen, len(original), "no contact skipped")
	for id, n := range seen {
		assert.True(t, original[id], "only contacts that existed when paging started")
		assert.Equal(t, 1, n, "no contact repeated")
	}

	first := listContacts(t, ce, ce.cookieA, url.Values{"limit": {"5"}})
	assert.True(t, strings.HasPrefix(first.Data[0].FullName, "Late"), "a fresh first page shows the new contacts")

	t.Run("invalid limit or cursor is 400", func(t *testing.T) {
		for _, q := range []string{"limit=0", "limit=101", "cursor=garbage"} {
			rec := call(t, ce.h, http.MethodGet, "/api/v1/contacts?"+q, nil, ce.cookieA)
			assert.Equal(t, http.StatusBadRequest, rec.Code, q)
		}
	})
}

// TestContactSearch proves FR-CON-03: case- and accent-insensitive substring
// search over name, email and phone.
func TestContactSearch(t *testing.T) {
	ce := newContactEnv(t, contact.NoDeals{})
	createContact(t, ce, ce.cookieA, map[string]any{"full_name": "Nguyễn Văn An"})
	createContact(t, ce, ce.cookieA, map[string]any{"full_name": "Trần Thị Bình", "email": "binh@acme.vn"})
	createContact(t, ce, ce.cookieA, map[string]any{"full_name": "Lê Hoà", "phone": "0901234567"})
	createContact(t, ce, ce.cookieA, map[string]any{"full_name": "Giảm 50% Shop"})
	createContact(t, ce, ce.cookieA, map[string]any{"full_name": "Giảm 500 Shop"})
	createContact(t, ce, ce.cookieB, map[string]any{"full_name": "Nguyễn Thị Khác"})

	tests := []struct {
		q    string
		want []string
	}{
		{q: "nguyen", want: []string{"Nguyễn Văn An"}},
		{q: "NGUYỄN VĂN", want: []string{"Nguyễn Văn An"}},
		{q: "van an", want: []string{"Nguyễn Văn An"}},
		{q: "binh@acme", want: []string{"Trần Thị Bình"}},
		{q: "234567", want: []string{"Lê Hoà"}},
		{q: "hoa", want: []string{"Lê Hoà"}},
		{q: "50%", want: []string{"Giảm 50% Shop"}},
		{q: "khac", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.q, func(t *testing.T) {
			p := listContacts(t, ce, ce.cookieA, url.Values{"q": {tt.q}})
			var got []string
			for _, c := range p.Data {
				got = append(got, c.FullName)
			}
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

// TestContactCRUD covers FR-CON-01/04, validation and the error format.
func TestContactCRUD(t *testing.T) {
	ce := newContactEnv(t, contact.NoDeals{})

	c := createContact(t, ce, ce.cookieA, map[string]any{"full_name": "An", "email": "AN@x.io", "phone": "0901"})
	assert.Equal(t, ce.s.userA, c.OwnerID, "owner defaults to the caller")

	t.Run("validation errors are 400", func(t *testing.T) {
		for name, body := range map[string]map[string]any{
			"missing full_name":  {"email": "a@x.io"},
			"full_name too long": {"full_name": strings.Repeat("a", 201)},
			"invalid email":      {"full_name": "A", "email": "nope"},
			"phone too long":     {"full_name": "A", "phone": strings.Repeat("1", 21)},
		} {
			rec := call(t, ce.h, http.MethodPost, "/api/v1/contacts", body, ce.cookieA)
			assert.Equal(t, http.StatusBadRequest, rec.Code, name)
			assert.Equal(t, "VALIDATION_ERROR", errorCode(t, rec), name)
		}
		rec := call(t, ce.h, http.MethodPatch, "/api/v1/contacts/"+c.ID.String(), map[string]any{"full_name": ""}, ce.cookieA)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "explicit empty full_name on PATCH")
	})

	t.Run("email unique per tenant, case-insensitive", func(t *testing.T) {
		rec := call(t, ce.h, http.MethodPost, "/api/v1/contacts", map[string]any{"full_name": "Dup", "email": "an@X.IO"}, ce.cookieA)
		require.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, "EMAIL_TAKEN", errorCode(t, rec))

		createContact(t, ce, ce.cookieB, map[string]any{"full_name": "Other tenant", "email": "an@x.io"})
	})

	t.Run("patch updates only the given fields", func(t *testing.T) {
		rec := call(t, ce.h, http.MethodPatch, "/api/v1/contacts/"+c.ID.String(), map[string]any{"company": "Acme", "phone": ""}, ce.cookieA)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var got contact.Contact
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, "An", got.FullName)
		assert.Equal(t, "Acme", *got.Company)
		assert.Nil(t, got.Phone)
		assert.True(t, got.UpdatedAt.After(c.UpdatedAt))
	})

	t.Run("soft delete hides the contact and frees its email", func(t *testing.T) {
		rec := call(t, ce.h, http.MethodDelete, "/api/v1/contacts/"+c.ID.String(), nil, ce.cookieA)
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

		assert.Equal(t, http.StatusNotFound, call(t, ce.h, http.MethodGet, "/api/v1/contacts/"+c.ID.String(), nil, ce.cookieA).Code)
		for _, item := range listContacts(t, ce, ce.cookieA, nil).Data {
			assert.NotEqual(t, c.ID, item.ID)
		}
		var deletedAt *time.Time
		err := ce.baseEnv.db.WithTenantTx(t.Context(), ce.s.tenantA, func(tx pgx.Tx) error {
			return tx.QueryRow(t.Context(), "SELECT deleted_at FROM contacts WHERE id = $1", c.ID).Scan(&deletedAt)
		})
		require.NoError(t, err)
		assert.NotNil(t, deletedAt, "row is kept, only marked deleted")

		createContact(t, ce, ce.cookieA, map[string]any{"full_name": "New An", "email": "an@x.io"})
	})

	t.Run("delete with open deals is 409", func(t *testing.T) {
		withDeals := newContactEnv(t, alwaysOpenDeals{})
		c := createContact(t, withDeals, withDeals.cookieA, map[string]any{"full_name": "Has deals"})

		rec := call(t, withDeals.h, http.MethodDelete, "/api/v1/contacts/"+c.ID.String(), nil, withDeals.cookieA)
		require.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, "CONTACT_HAS_OPEN_DEALS", errorCode(t, rec))
	})

	t.Run("no access token is 401", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, call(t, ce.h, http.MethodGet, "/api/v1/contacts", nil).Code)
	})
}

// TestContactTenantIsolation proves NFR-ISO-01/04 for contacts through the API.
func TestContactTenantIsolation(t *testing.T) {
	ce := newContactEnv(t, contact.NoDeals{})
	bContact := createContact(t, ce, ce.cookieB, map[string]any{"full_name": "Secret of B", "email": "secret@b.test"})
	path := "/api/v1/contacts/" + bContact.ID.String()

	tests := []struct {
		name       string
		method     string
		path       string
		body       any
		wantStatus int
		wantCode   string
	}{
		{name: "GET B's contact", method: http.MethodGet, path: path, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "PATCH B's contact", method: http.MethodPatch, path: path, body: map[string]any{"full_name": "hacked"},
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "DELETE B's contact", method: http.MethodDelete, path: path, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "malformed id looks like a missing one", method: http.MethodGet, path: "/api/v1/contacts/not-a-uuid",
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "owner from another tenant is 422", method: http.MethodPost, path: "/api/v1/contacts",
			body: map[string]any{"full_name": "X", "owner_id": ce.s.userB}, wantStatus: http.StatusUnprocessableEntity, wantCode: "INVALID_OWNER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := call(t, ce.h, tt.method, tt.path, tt.body, ce.cookieA)
			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			assert.Equal(t, tt.wantCode, errorCode(t, rec))
			assert.NotContains(t, rec.Body.String(), "secret@b.test")
		})
	}

	t.Run("A's list and search never show B's contacts", func(t *testing.T) {
		for _, q := range []url.Values{nil, {"q": {"secret"}}} {
			assert.Empty(t, listContacts(t, ce, ce.cookieA, q).Data)
		}
	})

	t.Run("B's contact is unchanged", func(t *testing.T) {
		rec := call(t, ce.h, http.MethodGet, path, nil, ce.cookieB)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "Secret of B")
	})
}
