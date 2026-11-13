//go:build integration

package integration

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HieuTrannn/crm-backend/internal/deal"
	"github.com/HieuTrannn/crm-backend/internal/platform/database"
)

func createDeal(t *testing.T, ae apiEnv, cookie *http.Cookie, body map[string]any) deal.Deal {
	t.Helper()
	rec := call(t, ae.h, http.MethodPost, "/api/v1/deals", body, cookie)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var d deal.Deal
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &d))
	return d
}

func transition(t *testing.T, ae apiEnv, d deal.Deal, body map[string]any) (int, deal.Deal) {
	t.Helper()
	body["version"] = d.Version
	rec := call(t, ae.h, http.MethodPost, "/api/v1/deals/"+d.ID.String()+"/transition", body, ae.cookieA)
	var got deal.Deal
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	}
	return rec.Code, got
}

// historyRow is the part of a history entry the pipeline test compares.
type historyRow struct {
	From   *deal.Stage
	To     deal.Stage
	Reason *string
}

// stageHistory reads GET /deals/{id}/history as tenant A.
func stageHistory(t *testing.T, ae apiEnv, dealID uuid.UUID) []historyRow {
	t.Helper()
	rec := call(t, ae.h, http.MethodGet, "/api/v1/deals/"+dealID.String()+"/history", nil, ae.cookieA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Data []deal.HistoryEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	out := make([]historyRow, len(body.Data))
	for i, e := range body.Data {
		assert.Equal(t, ae.s.userA, e.ChangedBy, "who")
		assert.False(t, e.ChangedAt.IsZero(), "when")
		out[i] = historyRow{From: e.FromStage, To: e.ToStage, Reason: e.Reason}
	}
	return out
}

func ptr[T any](v T) *T { return &v }

// TestDealPipeline covers FR-DEAL-01..03: creation, the state machine, reasons
// and the history written in the same transaction.
func TestDealPipeline(t *testing.T) {
	ae := newAPIEnv(t)
	c := createContact(t, ae, ae.cookieA, map[string]any{"full_name": "Buyer"})

	d := createDeal(t, ae, ae.cookieA, map[string]any{
		"contact_id": c.ID, "title": "Website", "amount": 150_000_000, "expected_close_date": "2026-12-31",
	})
	assert.Equal(t, deal.StageLead, d.Stage)
	assert.Equal(t, int64(150_000_000), d.Amount)
	assert.Equal(t, int32(1), d.Version)
	assert.Equal(t, ae.s.userA, d.OwnerID, "owner defaults to the caller")
	assert.Equal(t, "2026-12-31", d.ExpectedCloseDate.Format("2006-01-02"))

	t.Run("invalid transition is 422 and changes nothing", func(t *testing.T) {
		code, _ := transition(t, ae, d, map[string]any{"to_stage": "won"})
		require.Equal(t, http.StatusUnprocessableEntity, code)
	})

	t.Run("lost without a reason is 422", func(t *testing.T) {
		code, _ := transition(t, ae, d, map[string]any{"to_stage": "lost", "lost_reason": "   "})
		require.Equal(t, http.StatusUnprocessableEntity, code)
	})

	steps := []struct {
		body       map[string]any
		wantStage  deal.Stage
		wantClosed bool
	}{
		{map[string]any{"to_stage": "qualified"}, deal.StageQualified, false},
		{map[string]any{"to_stage": "lost", "lost_reason": "too expensive"}, deal.StageLost, true},
		{map[string]any{"to_stage": "lead", "reopen_reason": "new budget"}, deal.StageLead, false},
		{map[string]any{"to_stage": "qualified"}, deal.StageQualified, false},
		{map[string]any{"to_stage": "proposal"}, deal.StageProposal, false},
		{map[string]any{"to_stage": "won"}, deal.StageWon, true},
	}
	for _, step := range steps {
		code, got := transition(t, ae, d, step.body)
		require.Equal(t, http.StatusOK, code, "to %s", step.wantStage)
		assert.Equal(t, step.wantStage, got.Stage)
		assert.Equal(t, d.Version+1, got.Version, "each change bumps the version")
		assert.Equal(t, step.wantClosed, got.ClosedAt != nil)
		assert.Equal(t, step.wantStage == deal.StageLost, got.LostReason != nil, "lost_reason only while lost")
		d = got
	}

	t.Run("won is final", func(t *testing.T) {
		for _, to := range []string{"lead", "qualified", "proposal", "lost"} {
			code, _ := transition(t, ae, d, map[string]any{"to_stage": to, "lost_reason": "x"})
			assert.Equal(t, http.StatusUnprocessableEntity, code, to)
		}
	})

	t.Run("GET history matches every change, in order", func(t *testing.T) {
		assert.Equal(t, []historyRow{
			{From: nil, To: deal.StageLead},
			{From: ptr(deal.StageLead), To: deal.StageQualified},
			{From: ptr(deal.StageQualified), To: deal.StageLost, Reason: ptr("too expensive")},
			{From: ptr(deal.StageLost), To: deal.StageLead, Reason: ptr("new budget")},
			{From: ptr(deal.StageLead), To: deal.StageQualified},
			{From: ptr(deal.StageQualified), To: deal.StageProposal},
			{From: ptr(deal.StageProposal), To: deal.StageWon},
		}, stageHistory(t, ae, d.ID))
	})

	t.Run("failed transitions leave no history", func(t *testing.T) {
		before := len(stageHistory(t, ae, d.ID))
		code, _ := transition(t, ae, d, map[string]any{"to_stage": "lead"})
		require.Equal(t, http.StatusUnprocessableEntity, code)
		assert.Len(t, stageHistory(t, ae, d.ID), before)
	})
}

// TestDealOptimisticLocking covers FR-DEAL-04 through the API.
func TestDealOptimisticLocking(t *testing.T) {
	ae := newAPIEnv(t)
	c := createContact(t, ae, ae.cookieA, map[string]any{"full_name": "Buyer"})
	d := createDeal(t, ae, ae.cookieA, map[string]any{"contact_id": c.ID, "title": "Old", "amount": 10})
	path := "/api/v1/deals/" + d.ID.String()

	rec := call(t, ae.h, http.MethodPatch, path, map[string]any{"version": 1, "title": "First"}, ae.cookieA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	t.Run("stale version on PATCH is 409 with the latest deal", func(t *testing.T) {
		rec := call(t, ae.h, http.MethodPatch, path, map[string]any{"version": 1, "title": "Second"}, ae.cookieA)
		require.Equal(t, http.StatusConflict, rec.Code)
		var body struct {
			Error struct {
				Code    string    `json:"code"`
				Details deal.Deal `json:"details"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "VERSION_CONFLICT", body.Error.Code)
		assert.Equal(t, "First", body.Error.Details.Title)
		assert.Equal(t, int32(2), body.Error.Details.Version)
	})

	t.Run("stale version on transition is 409", func(t *testing.T) {
		rec := call(t, ae.h, http.MethodPost, path+"/transition", map[string]any{"version": 1, "to_stage": "qualified"}, ae.cookieA)
		require.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, "VERSION_CONFLICT", errorCode(t, rec))
	})

	t.Run("missing deal is 404, not 409", func(t *testing.T) {
		rec := call(t, ae.h, http.MethodPatch, "/api/v1/deals/"+uuid.NewString(), map[string]any{"version": 1, "title": "X"}, ae.cookieA)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("validation", func(t *testing.T) {
		for name, body := range map[string]map[string]any{
			"negative amount":   {"contact_id": c.ID, "title": "X", "amount": -1},
			"missing amount":    {"contact_id": c.ID, "title": "X"},
			"fractional amount": {"contact_id": c.ID, "title": "X", "amount": 1.5},
			"bad date":          {"contact_id": c.ID, "title": "X", "amount": 1, "expected_close_date": "31/12/2026"},
		} {
			assert.Equal(t, http.StatusBadRequest, call(t, ae.h, http.MethodPost, "/api/v1/deals", body, ae.cookieA).Code, name)
		}
		assert.Equal(t, http.StatusBadRequest,
			call(t, ae.h, http.MethodPatch, path, map[string]any{"title": "no version"}, ae.cookieA).Code, "version is required")
	})
}

// TestDealConcurrentUpdates is FR-DEAL-04's concurrency test: 10 goroutines
// update the same deal with the same version at once; exactly one wins.
// Run with -race (make test-integration).
func TestDealConcurrentUpdates(t *testing.T) {
	ae := newAPIEnv(t)
	c := createContact(t, ae, ae.cookieA, map[string]any{"full_name": "Buyer"})
	d := createDeal(t, ae, ae.cookieA, map[string]any{"contact_id": c.ID, "title": "Contended", "amount": 1})

	// A pool with a connection per goroutine, so the updates really overlap in PostgreSQL.
	cfg, err := pgxpool.ParseConfig(ae.baseEnv.appURL)
	require.NoError(t, err)
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	svc := deal.NewService(deal.NewPostgresRepository(database.New(pool)))

	const workers = 10
	errs := make([]error, workers) // each goroutine writes only its own slot: no data race
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines together
			amount := int64(i)
			_, errs[i] = svc.Update(t.Context(), ae.s.tenantA, d.ID, deal.UpdateInput{Version: d.Version, Amount: &amount})
		}()
	}
	close(start)
	wg.Wait()

	var ok, conflicts int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, deal.ErrVersionConflict):
			conflicts++
			var vc *deal.VersionConflictError
			require.ErrorAs(t, err, &vc)
			assert.Equal(t, int32(2), vc.Latest.Version, "conflict carries the winner's version")
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	assert.Equal(t, 1, ok, "exactly one update wins")
	assert.Equal(t, workers-1, conflicts, "all others get ErrVersionConflict")

	final, err := svc.Get(t.Context(), ae.s.tenantA, d.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(2), final.Version, "the version moved exactly once")
}

// TestDealListAndIsolation covers FR-DEAL-05 filters and NFR-ISO-01/04 for deals.
func TestDealListAndIsolation(t *testing.T) {
	ae := newAPIEnv(t)
	c1 := createContact(t, ae, ae.cookieA, map[string]any{"full_name": "One"})
	c2 := createContact(t, ae, ae.cookieA, map[string]any{"full_name": "Two"})
	for i := range 3 {
		createDeal(t, ae, ae.cookieA, map[string]any{"contact_id": c1.ID, "title": "c1", "amount": i})
	}
	d2 := createDeal(t, ae, ae.cookieA, map[string]any{"contact_id": c2.ID, "title": "c2", "amount": 9})
	_, _ = transition(t, ae, d2, map[string]any{"to_stage": "qualified"})

	bContact := createContact(t, ae, ae.cookieB, map[string]any{"full_name": "B buyer"})
	bDeal := createDeal(t, ae, ae.cookieB, map[string]any{"contact_id": bContact.ID, "title": "Secret B deal", "amount": 99})

	list := func(q url.Values) []deal.Deal {
		rec := call(t, ae.h, http.MethodGet, "/api/v1/deals?"+q.Encode(), nil, ae.cookieA)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var p struct {
			Data       []deal.Deal `json:"data"`
			NextCursor *string     `json:"next_cursor"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
		return p.Data
	}

	t.Run("filters", func(t *testing.T) {
		assert.Len(t, list(nil), 4, "only A's deals")
		assert.Len(t, list(url.Values{"contact_id": {c1.ID.String()}}), 3)
		assert.Len(t, list(url.Values{"stage": {"qualified"}}), 1)
		assert.Len(t, list(url.Values{"stage": {"lead"}, "contact_id": {c2.ID.String()}}), 0)
		assert.Len(t, list(url.Values{"owner_id": {ae.s.userA.String()}}), 4)
		for _, q := range []string{"stage=closed", "owner_id=nope", "contact_id=nope"} {
			assert.Equal(t, http.StatusBadRequest, call(t, ae.h, http.MethodGet, "/api/v1/deals?"+q, nil, ae.cookieA).Code, q)
		}
	})

	t.Run("cursor pagination", func(t *testing.T) {
		seen := map[uuid.UUID]bool{}
		q := url.Values{"limit": {"3"}}
		for {
			rec := call(t, ae.h, http.MethodGet, "/api/v1/deals?"+q.Encode(), nil, ae.cookieA)
			var p struct {
				Data       []deal.Deal `json:"data"`
				NextCursor *string     `json:"next_cursor"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			for _, d := range p.Data {
				assert.False(t, seen[d.ID], "no repeats")
				seen[d.ID] = true
			}
			if p.NextCursor == nil {
				break
			}
			q.Set("cursor", *p.NextCursor)
		}
		assert.Len(t, seen, 4)
	})

	bPath := "/api/v1/deals/" + bDeal.ID.String()
	tests := []struct {
		name     string
		method   string
		path     string
		body     any
		want     int
		wantCode string
	}{
		{"GET B's deal", http.MethodGet, bPath, nil, http.StatusNotFound, "NOT_FOUND"},
		{"PATCH B's deal", http.MethodPatch, bPath, map[string]any{"version": 1, "title": "hacked"}, http.StatusNotFound, "NOT_FOUND"},
		{"history of B's deal", http.MethodGet, bPath + "/history", nil, http.StatusNotFound, "NOT_FOUND"},
		{"DELETE B's deal", http.MethodDelete, bPath + "?version=1", nil, http.StatusNotFound, "NOT_FOUND"},
		{"transition B's deal", http.MethodPost, bPath + "/transition", map[string]any{"version": 1, "to_stage": "qualified"},
			http.StatusNotFound, "NOT_FOUND"},
		{"deal for B's contact", http.MethodPost, "/api/v1/deals", map[string]any{"contact_id": bContact.ID, "title": "X", "amount": 1},
			http.StatusUnprocessableEntity, "INVALID_CONTACT"},
		{"owner from B", http.MethodPost, "/api/v1/deals", map[string]any{"contact_id": c1.ID, "title": "X", "amount": 1, "owner_id": ae.s.userB},
			http.StatusUnprocessableEntity, "INVALID_OWNER"},
	}
	for _, tt := range tests {
		t.Run("isolation/"+tt.name, func(t *testing.T) {
			rec := call(t, ae.h, tt.method, tt.path, tt.body, ae.cookieA)
			require.Equal(t, tt.want, rec.Code, rec.Body.String())
			assert.Equal(t, tt.wantCode, errorCode(t, rec))
			assert.NotContains(t, rec.Body.String(), "Secret B deal")
		})
	}

	rec := call(t, ae.h, http.MethodGet, bPath, nil, ae.cookieB)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Secret B deal", "B's deal is unchanged")
}

// TestDealDelete covers FR-DEAL-06: soft delete guarded by version.
func TestDealDelete(t *testing.T) {
	ae := newAPIEnv(t)
	c := createContact(t, ae, ae.cookieA, map[string]any{"full_name": "Buyer"})
	d := createDeal(t, ae, ae.cookieA, map[string]any{"contact_id": c.ID, "title": "Duplicate", "amount": 5})
	path := "/api/v1/deals/" + d.ID.String()

	t.Run("version is required", func(t *testing.T) {
		for _, q := range []string{"", "?version=0", "?version=abc"} {
			assert.Equal(t, http.StatusBadRequest, call(t, ae.h, http.MethodDelete, path+q, nil, ae.cookieA).Code, q)
		}
	})

	t.Run("stale version is 409 with the latest deal", func(t *testing.T) {
		rec := call(t, ae.h, http.MethodDelete, path+"?version=7", nil, ae.cookieA)
		require.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, "VERSION_CONFLICT", errorCode(t, rec))
		assert.Contains(t, rec.Body.String(), `"title":"Duplicate"`)
	})

	t.Run("open deal blocks deleting its contact until the deal is deleted", func(t *testing.T) {
		contactPath := "/api/v1/contacts/" + c.ID.String()
		require.Equal(t, http.StatusConflict, call(t, ae.h, http.MethodDelete, contactPath, nil, ae.cookieA).Code)

		rec := call(t, ae.h, http.MethodDelete, path+"?version=1", nil, ae.cookieA)
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

		require.Equal(t, http.StatusNoContent, call(t, ae.h, http.MethodDelete, contactPath, nil, ae.cookieA).Code)
	})

	t.Run("a deleted deal is gone from every endpoint", func(t *testing.T) {
		for name, rec := range map[string]int{
			"get":        call(t, ae.h, http.MethodGet, path, nil, ae.cookieA).Code,
			"history":    call(t, ae.h, http.MethodGet, path+"/history", nil, ae.cookieA).Code,
			"patch":      call(t, ae.h, http.MethodPatch, path, map[string]any{"version": 2, "title": "X"}, ae.cookieA).Code,
			"transition": call(t, ae.h, http.MethodPost, path+"/transition", map[string]any{"version": 2, "to_stage": "qualified"}, ae.cookieA).Code,
			"delete":     call(t, ae.h, http.MethodDelete, path+"?version=2", nil, ae.cookieA).Code,
		} {
			assert.Equal(t, http.StatusNotFound, rec, name)
		}
		rec := call(t, ae.h, http.MethodGet, "/api/v1/deals", nil, ae.cookieA)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), d.ID.String())
	})
}
