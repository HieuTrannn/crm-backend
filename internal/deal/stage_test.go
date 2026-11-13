package deal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCanTransition checks every (from, to) pair, valid or not, against the PRD
// table written out independently of the transitions() map under test.
func TestCanTransition(t *testing.T) {
	allowed := map[[2]Stage]bool{
		{StageLead, StageQualified}:     true,
		{StageLead, StageLost}:          true,
		{StageQualified, StageProposal}: true,
		{StageQualified, StageLead}:     true,
		{StageQualified, StageLost}:     true,
		{StageProposal, StageWon}:       true,
		{StageProposal, StageLost}:      true,
		{StageProposal, StageQualified}: true,
		{StageLost, StageLead}:          true,
	}
	// The five stages plus values that must never be accepted.
	stages := []Stage{StageLead, StageQualified, StageProposal, StageWon, StageLost, "", "closed", "LEAD"}

	tested := 0
	for _, from := range stages {
		for _, to := range stages {
			want := allowed[[2]Stage{from, to}]
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				assert.Equal(t, want, CanTransition(from, to))
			})
			tested++
		}
	}
	assert.Equal(t, len(stages)*len(stages), tested)
	assert.Len(t, allowed, 9, "PRD 3.3 lists 9 allowed transitions")
}

func TestStageValidAndOpen(t *testing.T) {
	tests := []struct {
		stage Stage
		valid bool
		open  bool
	}{
		{StageLead, true, true},
		{StageQualified, true, true},
		{StageProposal, true, true},
		{StageWon, true, false},
		{StageLost, true, false},
		{"closed", false, true},
		{"", false, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.stage), func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.stage.Valid())
			if tt.valid {
				assert.Equal(t, tt.open, tt.stage.Open())
			}
		})
	}
}
