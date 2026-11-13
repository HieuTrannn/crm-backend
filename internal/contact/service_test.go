package contact

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRepo keeps contacts in memory. RLS, SQL search and constraints are covered
// by the integration tests; here only the service's rules are under test.
type fakeRepo struct {
	rows      map[uuid.UUID]*fakeRow
	lastQuery string
	clock     time.Time
}

type fakeRow struct {
	Contact
	tenantID uuid.UUID
	deleted  bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[uuid.UUID]*fakeRow{}, clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeRepo) WithTenant(_ context.Context, tenantID uuid.UUID, fn func(Store) error) error {
	return fn(&fakeStore{f: f, tenantID: tenantID})
}

type fakeStore struct {
	f        *fakeRepo
	tenantID uuid.UUID
}

func (s *fakeStore) Create(_ context.Context, id uuid.UUID, in CreateInput) (Contact, error) {
	s.f.clock = s.f.clock.Add(time.Second)
	c := Contact{ID: id, FullName: in.FullName, Email: in.Email, Phone: in.Phone, Company: in.Company,
		Notes: in.Notes, OwnerID: *in.OwnerID, CreatedAt: s.f.clock, UpdatedAt: s.f.clock}
	s.f.rows[id] = &fakeRow{Contact: c, tenantID: s.tenantID}
	return c, nil
}

func (s *fakeStore) Get(_ context.Context, id uuid.UUID) (Contact, error) {
	r, ok := s.f.rows[id]
	if !ok || r.deleted || r.tenantID != s.tenantID {
		return Contact{}, ErrNotFound
	}
	return r.Contact, nil
}

func (s *fakeStore) GetForUpdate(ctx context.Context, id uuid.UUID) (Contact, error) {
	return s.Get(ctx, id)
}

func (s *fakeStore) Update(_ context.Context, c Contact) (Contact, error) {
	s.f.rows[c.ID].Contact = c
	return c, nil
}

func (s *fakeStore) SoftDelete(_ context.Context, id uuid.UUID) error {
	s.f.rows[id].deleted = true
	return nil
}

func (s *fakeStore) List(_ context.Context, p ListParams) ([]Contact, error) {
	s.f.lastQuery = p.Query
	var out []Contact
	for _, r := range s.f.rows {
		if r.tenantID == s.tenantID && !r.deleted && strings.Contains(strings.ToLower(r.FullName), strings.ToLower(p.Query)) {
			out = append(out, r.Contact)
		}
	}
	slices.SortFunc(out, func(a, b Contact) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out[:min(len(out), p.Limit)], nil
}

type stubDeals struct{ open bool }

func (d stubDeals) HasOpenDeals(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return d.open, nil
}

func ptr[T any](v T) *T { return &v }

var (
	tenantID = uuid.MustParse("0190a000-0000-7000-8000-000000000001")
	callerID = uuid.MustParse("0190a000-0000-7000-8000-0000000000aa")
)

func TestCreate_DefaultsOwnerAndStoresBlankAsNull(t *testing.T) {
	svc := NewService(newFakeRepo(), NoDeals{})

	c, err := svc.Create(t.Context(), tenantID, callerID, CreateInput{
		FullName: "Nguyễn Văn A", Email: ptr(""), Phone: ptr("  "), Company: ptr("Acme"),
	})

	require.NoError(t, err)
	assert.Equal(t, callerID, c.OwnerID, "owner defaults to the caller")
	assert.Nil(t, c.Email)
	assert.Nil(t, c.Phone)
	assert.Equal(t, "Acme", *c.Company)
}

func TestUpdate_IsPartial(t *testing.T) {
	svc := NewService(newFakeRepo(), NoDeals{})
	c, err := svc.Create(t.Context(), tenantID, callerID, CreateInput{
		FullName: "An", Email: ptr("an@x.io"), Phone: ptr("0901"),
	})
	require.NoError(t, err)

	got, err := svc.Update(t.Context(), tenantID, c.ID, UpdateInput{Email: ptr(""), Company: ptr("Beta")})

	require.NoError(t, err)
	assert.Equal(t, "An", got.FullName, "absent field unchanged")
	assert.Nil(t, got.Email, `"" clears an optional field`)
	assert.Equal(t, "0901", *got.Phone)
	assert.Equal(t, "Beta", *got.Company)
}

func TestUpdate_NotFound(t *testing.T) {
	svc := NewService(newFakeRepo(), NoDeals{})

	_, err := svc.Update(t.Context(), tenantID, uuid.Must(uuid.NewV7()), UpdateInput{FullName: ptr("X")})

	require.ErrorIs(t, err, ErrNotFound)
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name      string
		openDeals bool
		wantErr   error
	}{
		{name: "no open deals: soft deleted"},
		{name: "open deals: refused", openDeals: true, wantErr: ErrHasOpenDeals},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(newFakeRepo(), stubDeals{open: tt.openDeals})
			c, err := svc.Create(t.Context(), tenantID, callerID, CreateInput{FullName: "An"})
			require.NoError(t, err)

			err = svc.Delete(t.Context(), tenantID, c.ID)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				_, err = svc.Get(t.Context(), tenantID, c.ID)
				require.NoError(t, err, "contact still visible")
				return
			}
			require.NoError(t, err)
			_, err = svc.Get(t.Context(), tenantID, c.ID)
			require.ErrorIs(t, err, ErrNotFound, "deleted contact is gone")
			require.ErrorIs(t, svc.Delete(t.Context(), tenantID, c.ID), ErrNotFound)
		})
	}
}

func TestList_HasMoreAndEscapesQuery(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, NoDeals{})
	for _, name := range []string{"An", "Bình", "Chi"} {
		_, err := svc.Create(t.Context(), tenantID, callerID, CreateInput{FullName: name})
		require.NoError(t, err)
	}

	page, hasMore, err := svc.List(t.Context(), tenantID, ListParams{Limit: 2})
	require.NoError(t, err)
	assert.True(t, hasMore)
	assert.Equal(t, []string{"Chi", "Bình"}, names(page), "newest first")

	page, hasMore, err = svc.List(t.Context(), tenantID, ListParams{Limit: 3})
	require.NoError(t, err)
	assert.False(t, hasMore, "exactly limit rows means no next page")
	assert.Len(t, page, 3)

	_, _, err = svc.List(t.Context(), tenantID, ListParams{Query: "  50%_off\\ ", Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, `50\%\_off\\`, repo.lastQuery, "LIKE wildcards in user input match literally")
}

func names(cs []Contact) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.FullName
	}
	return out
}
