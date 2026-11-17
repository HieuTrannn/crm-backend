//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HieuTrannn/crm-backend/internal/dashboard"
)

// fixtureDeal is inserted directly, so closed_at can be set to exact instants.
type fixtureDeal struct {
	stage    string
	amount   int64
	closedAt string // RFC 3339; empty for open deals
	deleted  bool
}

// insertDeals writes one contact and the given deals for a tenant as app_user.
func insertDeals(t *testing.T, e env, tenantID, ownerID uuid.UUID, deals []fixtureDeal) {
	t.Helper()
	err := e.db.WithTenantTx(t.Context(), tenantID, func(tx pgx.Tx) error {
		contactID := uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(t.Context(),
			"INSERT INTO contacts (id, tenant_id, full_name, owner_id) VALUES ($1, $2, 'Fixture', $3)",
			contactID, tenantID, ownerID); err != nil {
			return err
		}
		for _, d := range deals {
			var closedAt, deletedAt *time.Time
			var lostReason *string
			if d.closedAt != "" {
				ts, err := time.Parse(time.RFC3339, d.closedAt)
				require.NoError(t, err)
				closedAt = &ts
			}
			if d.deleted {
				now := time.Now()
				deletedAt = &now
			}
			if d.stage == "lost" {
				reason := "fixture"
				lostReason = &reason
			}
			if _, err := tx.Exec(t.Context(), `INSERT INTO deals
				(id, tenant_id, contact_id, title, amount, stage, owner_id, lost_reason, closed_at, deleted_at)
				VALUES ($1, $2, $3, 'Fixture', $4, $5, $6, $7, $8, $9)`,
				uuid.Must(uuid.NewV7()), tenantID, contactID, d.amount, d.stage, ownerID, lostReason, closedAt, deletedAt); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// seedDashboard: tenant A gets a known set of deals, tenant B gets deals that
// must never show up in A's figures.
func seedDashboard(t *testing.T, ae apiEnv) {
	t.Helper()
	insertDeals(t, ae.baseEnv, ae.s.tenantA, ae.s.userA, []fixtureDeal{
		{stage: "lead", amount: 100},
		{stage: "lead", amount: 200},
		{stage: "qualified", amount: 300},
		// Vietnam is UTC+7: these two straddle the October/November boundary.
		{stage: "won", amount: 4_000, closedAt: "2026-10-31T16:59:59Z"}, // 2026-10-31 23:59:59 ICT
		{stage: "won", amount: 2_000, closedAt: "2026-10-31T17:30:00Z"}, // 2026-11-01 00:30 ICT
		{stage: "won", amount: 1_000, closedAt: "2026-11-10T05:00:00Z"},
		{stage: "lost", amount: 500, closedAt: "2026-10-15T00:00:00Z"},
		{stage: "lost", amount: 700, closedAt: "2026-08-01T00:00:00Z"},
		{stage: "won", amount: 8_000, closedAt: "2026-05-20T00:00:00Z"},                 // before the 6-month window
		{stage: "won", amount: 16_000, closedAt: "2026-11-05T00:00:00Z", deleted: true}, // soft-deleted: ignored
	})
	insertDeals(t, ae.baseEnv, ae.s.tenantB, ae.s.userB, []fixtureDeal{
		{stage: "lead", amount: 999_999},
		{stage: "won", amount: 999_999, closedAt: "2026-11-10T05:00:00Z"},
	})
}

// TestDashboardPipeline covers FR-DASH-01 through the API.
func TestDashboardPipeline(t *testing.T) {
	ae := newAPIEnv(t)
	seedDashboard(t, ae)

	rec := call(t, ae.h, http.MethodGet, "/api/v1/dashboard/pipeline", nil, ae.cookieA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Data []dashboard.StageSummary `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, []dashboard.StageSummary{
		{Stage: "lead", DealCount: 2, TotalAmount: 300},
		{Stage: "qualified", DealCount: 1, TotalAmount: 300},
		{Stage: "proposal", DealCount: 0, TotalAmount: 0}, // empty stage still listed
		{Stage: "won", DealCount: 4, TotalAmount: 15_000}, // deleted deal and tenant B excluded
		{Stage: "lost", DealCount: 2, TotalAmount: 1_200},
	}, body.Data)

	t.Run("a tenant without deals gets all stages at zero", func(t *testing.T) {
		other := newAPIEnv(t)
		rec := call(t, other.h, http.MethodGet, "/api/v1/dashboard/pipeline", nil, other.cookieA)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body.Data, 5)
		for _, s := range body.Data {
			assert.Zero(t, s.DealCount, s.Stage)
			assert.Zero(t, s.TotalAmount, s.Stage)
		}
	})

	t.Run("no access token is 401", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, call(t, ae.h, http.MethodGet, "/api/v1/dashboard/pipeline", nil).Code)
	})
}

// TestDashboardMonthly covers FR-DASH-02 with a fixed "now", through the
// repository (the HTTP handler always uses the real clock).
func TestDashboardMonthly(t *testing.T) {
	ae := newAPIEnv(t)
	seedDashboard(t, ae)
	repo := dashboard.NewPostgresRepository(ae.baseEnv.db)

	monthly := func(asOf string) []dashboard.MonthSummary {
		ts, err := time.Parse(time.RFC3339, asOf)
		require.NoError(t, err)
		var out []dashboard.MonthSummary
		err = repo.WithTenant(t.Context(), ae.s.tenantA, func(st dashboard.Store) error {
			var err error
			out, err = st.Monthly(t.Context(), ts)
			return err
		})
		require.NoError(t, err)
		return out
	}

	tests := []struct {
		name string
		asOf string
		want []dashboard.MonthSummary
	}{
		{
			name: "six months ending in November, empty months are zero",
			asOf: "2026-11-17T03:00:00Z",
			want: []dashboard.MonthSummary{
				{Month: "2026-06"},
				{Month: "2026-07"},
				{Month: "2026-08", LostCount: 1},
				{Month: "2026-09"},
				{Month: "2026-10", WonCount: 1, LostCount: 1, WonAmount: 4_000, WinRate: 0.5},
				{Month: "2026-11", WonCount: 2, WonAmount: 3_000, WinRate: 1},
			},
		},
		{
			name: "window crosses the year boundary",
			asOf: "2027-02-10T00:00:00Z",
			want: []dashboard.MonthSummary{
				{Month: "2026-09"},
				{Month: "2026-10", WonCount: 1, LostCount: 1, WonAmount: 4_000, WinRate: 0.5},
				{Month: "2026-11", WonCount: 2, WonAmount: 3_000, WinRate: 1},
				{Month: "2026-12"},
				{Month: "2027-01"},
				{Month: "2027-02"},
			},
		},
		{
			// 2026-11-30T17:30Z is already 1 December in Vietnam.
			name: "the current month follows Vietnam time, not UTC",
			asOf: "2026-11-30T17:30:00Z",
			want: []dashboard.MonthSummary{
				{Month: "2026-07"},
				{Month: "2026-08", LostCount: 1},
				{Month: "2026-09"},
				{Month: "2026-10", WonCount: 1, LostCount: 1, WonAmount: 4_000, WinRate: 0.5},
				{Month: "2026-11", WonCount: 2, WonAmount: 3_000, WinRate: 1},
				{Month: "2026-12"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, monthly(tt.asOf))
		})
	}

	t.Run("HTTP returns six months ending with the current one", func(t *testing.T) {
		rec := call(t, ae.h, http.MethodGet, "/api/v1/dashboard/monthly", nil, ae.cookieA)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body struct {
			Data []dashboard.MonthSummary `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body.Data, 6)
		vn, err := time.LoadLocation("Asia/Ho_Chi_Minh")
		require.NoError(t, err)
		assert.Equal(t, time.Now().In(vn).Format("2006-01"), body.Data[5].Month)
	})

	t.Run("tenant B only sees its own deal", func(t *testing.T) {
		var out []dashboard.MonthSummary
		ts, _ := time.Parse(time.RFC3339, "2026-11-17T03:00:00Z")
		err := repo.WithTenant(t.Context(), ae.s.tenantB, func(st dashboard.Store) error {
			var err error
			out, err = st.Monthly(t.Context(), ts)
			return err
		})
		require.NoError(t, err)
		assert.Equal(t, dashboard.MonthSummary{Month: "2026-11", WonCount: 1, WonAmount: 999_999, WinRate: 1}, out[5])
	})
}
