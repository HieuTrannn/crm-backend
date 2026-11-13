package deal

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRepo keeps deals in memory. WithTenant holds a mutex for the whole
// callback, like a transaction that serializes conflicting writers; SQL, RLS
// and real concurrency are covered by the integration tests.
type fakeRepo struct {
	mu       sync.Mutex
	deals    map[uuid.UUID]Deal
	history  []stageChange
	contacts map[uuid.UUID]bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{deals: map[uuid.UUID]Deal{}, contacts: map[uuid.UUID]bool{contactID: true}}
}

func (f *fakeRepo) WithTenant(_ context.Context, _ uuid.UUID, fn func(Store) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(f)
}

func (f *fakeRepo) LockLiveContact(_ context.Context, id uuid.UUID) error {
	if !f.contacts[id] {
		return ErrInvalidContact
	}
	return nil
}

func (f *fakeRepo) Create(_ context.Context, id uuid.UUID, in CreateInput) (Deal, error) {
	d := Deal{ID: id, ContactID: in.ContactID, Title: in.Title, Amount: in.Amount, Stage: StageLead,
		ExpectedCloseDate: in.ExpectedCloseDate, OwnerID: *in.OwnerID, Version: 1}
	f.deals[id] = d
	return d, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (Deal, error) {
	d, ok := f.deals[id]
	if !ok {
		return Deal{}, ErrNotFound
	}
	return d, nil
}

func (f *fakeRepo) Update(_ context.Context, d Deal, version int32) (Deal, error) {
	if cur, ok := f.deals[d.ID]; !ok || cur.Version != version {
		return Deal{}, errStale
	}
	d.Version = version + 1
	f.deals[d.ID] = d
	return d, nil
}

func (f *fakeRepo) SetStage(_ context.Context, id uuid.UUID, version int32, to Stage, lostReason *string) (Deal, error) {
	d, ok := f.deals[id]
	if !ok || d.Version != version {
		return Deal{}, errStale
	}
	d.Stage, d.LostReason, d.Version = to, lostReason, version+1
	f.deals[id] = d
	return d, nil
}

func (f *fakeRepo) SoftDelete(_ context.Context, id uuid.UUID, version int32) error {
	if d, ok := f.deals[id]; !ok || d.Version != version {
		return errStale
	}
	delete(f.deals, id)
	return nil
}

func (f *fakeRepo) AddStageChange(_ context.Context, c stageChange) error {
	f.history = append(f.history, c)
	return nil
}

func (f *fakeRepo) History(_ context.Context, dealID uuid.UUID) ([]HistoryEntry, error) {
	var out []HistoryEntry
	for _, c := range f.history {
		if c.DealID == dealID {
			out = append(out, HistoryEntry{FromStage: c.From, ToStage: c.To, Reason: c.Reason, ChangedBy: c.ChangedBy})
		}
	}
	return out, nil
}

func (f *fakeRepo) List(context.Context, ListParams) ([]Deal, error) { return nil, nil }

var (
	tenantID  = uuid.MustParse("0190a000-0000-7000-8000-000000000001")
	callerID  = uuid.MustParse("0190a000-0000-7000-8000-0000000000aa")
	contactID = uuid.MustParse("0190a000-0000-7000-8000-0000000000cc")
)

func newDeal(t *testing.T) (*Service, *fakeRepo, Deal) {
	t.Helper()
	repo := newFakeRepo()
	svc := NewService(repo)
	d, err := svc.Create(t.Context(), tenantID, callerID, CreateInput{ContactID: contactID, Title: "Deal", Amount: 100})
	require.NoError(t, err)
	return svc, repo, d
}

func TestCreate(t *testing.T) {
	svc, repo, d := newDeal(t)

	assert.Equal(t, StageLead, d.Stage)
	assert.Equal(t, callerID, d.OwnerID, "owner defaults to the caller")
	require.Len(t, repo.history, 1)
	assert.Nil(t, repo.history[0].From, "creation is recorded as NULL -> lead")
	assert.Equal(t, StageLead, repo.history[0].To)

	_, err := svc.Create(t.Context(), tenantID, callerID, CreateInput{ContactID: uuid.New(), Title: "X"})
	require.ErrorIs(t, err, ErrInvalidContact)
}

func TestTransition(t *testing.T) {
	tests := []struct {
		name    string
		path    []TransitionInput // applied in order before the checked step
		step    TransitionInput
		wantErr error
	}{
		{name: "lead to qualified", step: TransitionInput{To: StageQualified}},
		{name: "lead to won is invalid", step: TransitionInput{To: StageWon}, wantErr: ErrInvalidTransition},
		{name: "lost needs a reason", step: TransitionInput{To: StageLost, LostReason: "  "}, wantErr: ErrReasonRequired},
		{name: "lost with a reason", step: TransitionInput{To: StageLost, LostReason: "budget"}},
		{name: "reopen needs a reason", path: []TransitionInput{{To: StageLost, LostReason: "budget"}},
			step: TransitionInput{To: StageLead}, wantErr: ErrReasonRequired},
		{name: "reopen with a reason", path: []TransitionInput{{To: StageLost, LostReason: "budget"}},
			step: TransitionInput{To: StageLead, ReopenReason: "new budget"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo, d := newDeal(t)
			for _, in := range tt.path {
				in.Version = d.Version
				var err error
				d, err = svc.Transition(t.Context(), tenantID, callerID, d.ID, in)
				require.NoError(t, err)
			}
			tt.step.Version = d.Version
			historyBefore := len(repo.history)

			got, err := svc.Transition(t.Context(), tenantID, callerID, d.ID, tt.step)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Len(t, repo.history, historyBefore, "nothing recorded on failure")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.step.To, got.Stage)
			assert.Equal(t, d.Version+1, got.Version)
			assert.Equal(t, tt.step.To == StageLost, got.LostReason != nil, "lost_reason only while lost")
			last := repo.history[len(repo.history)-1]
			assert.Equal(t, d.Stage, *last.From)
			assert.Equal(t, tt.step.To, last.To)
		})
	}
}

func TestDelete(t *testing.T) {
	svc, _, d := newDeal(t)

	err := svc.Delete(t.Context(), tenantID, d.ID, d.Version+1)
	var vc *VersionConflictError
	require.ErrorAs(t, err, &vc, "stale version is a conflict")
	assert.Equal(t, d.Version, vc.Latest.Version)

	require.NoError(t, svc.Delete(t.Context(), tenantID, d.ID, d.Version))
	_, err = svc.Get(t.Context(), tenantID, d.ID)
	require.ErrorIs(t, err, ErrNotFound, "a deleted deal is gone")
	require.ErrorIs(t, svc.Delete(t.Context(), tenantID, d.ID, d.Version), ErrNotFound, "deleting twice is 404")
}

func TestHistory(t *testing.T) {
	svc, _, d := newDeal(t)
	d, err := svc.Transition(t.Context(), tenantID, callerID, d.ID, TransitionInput{To: StageLost, Version: d.Version, LostReason: "budget"})
	require.NoError(t, err)

	got, err := svc.History(t.Context(), tenantID, d.ID)

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Nil(t, got[0].FromStage)
	assert.Equal(t, StageLead, got[0].ToStage)
	assert.Equal(t, StageLead, *got[1].FromStage)
	assert.Equal(t, StageLost, got[1].ToStage)
	assert.Equal(t, "budget", *got[1].Reason)
	assert.Equal(t, callerID, got[1].ChangedBy)

	_, err = svc.History(t.Context(), tenantID, uuid.New())
	require.ErrorIs(t, err, ErrNotFound, "unknown deal is 404, not an empty history")
}

func TestVersionConflictCarriesLatest(t *testing.T) {
	svc, _, d := newDeal(t)
	title := "renamed"
	_, err := svc.Update(t.Context(), tenantID, d.ID, UpdateInput{Version: 1, Title: &title})
	require.NoError(t, err)

	_, errUpdate := svc.Update(t.Context(), tenantID, d.ID, UpdateInput{Version: 1, Title: &title})
	_, errMove := svc.Transition(t.Context(), tenantID, callerID, d.ID, TransitionInput{To: StageQualified, Version: 1})

	for _, err := range []error{errUpdate, errMove} {
		var vc *VersionConflictError
		require.ErrorAs(t, err, &vc)
		require.ErrorIs(t, err, ErrVersionConflict)
		assert.Equal(t, int32(2), vc.Latest.Version)
		assert.Equal(t, "renamed", vc.Latest.Title)
	}

	_, err = svc.Update(t.Context(), tenantID, uuid.New(), UpdateInput{Version: 1, Title: &title})
	require.ErrorIs(t, err, ErrNotFound, "a missing deal is 404, not a conflict")
}

// TestUpdate_Concurrent runs the service from 10 goroutines with one version;
// under -race it also checks the service shares no unsynchronized state.
func TestUpdate_Concurrent(t *testing.T) {
	svc, _, d := newDeal(t)
	const workers = 10
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			amount := int64(i)
			_, errs[i] = svc.Update(t.Context(), tenantID, d.ID, UpdateInput{Version: d.Version, Amount: &amount})
		}()
	}
	wg.Wait()

	var ok, conflicts int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrVersionConflict):
			conflicts++
		}
	}
	assert.Equal(t, 1, ok)
	assert.Equal(t, workers-1, conflicts)
}
