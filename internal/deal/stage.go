// Package deal manages deals and their sales pipeline (FR-DEAL-01..05).
package deal

// Stage is a step of the sales pipeline.
type Stage string

// Pipeline stages: lead -> qualified -> proposal -> won | lost.
const (
	StageLead      Stage = "lead"
	StageQualified Stage = "qualified"
	StageProposal  Stage = "proposal"
	StageWon       Stage = "won"
	StageLost      Stage = "lost"
)

// transitions is the state machine of PRD 3.3: the stages each stage may move to.
// won is final; lost can only be reopened to lead. It is a function rather than a
// package variable so the table cannot be modified at runtime.
func transitions() map[Stage][]Stage {
	return map[Stage][]Stage{
		StageLead:      {StageQualified, StageLost},
		StageQualified: {StageProposal, StageLead, StageLost},
		StageProposal:  {StageWon, StageLost, StageQualified},
		StageWon:       {},
		StageLost:      {StageLead},
	}
}

// CanTransition reports whether a deal may move from one stage to another.
// Unknown stages and staying in the same stage are never allowed.
func CanTransition(from, to Stage) bool {
	for _, next := range transitions()[from] {
		if next == to {
			return true
		}
	}
	return false
}

// Valid reports whether s is one of the five pipeline stages.
func (s Stage) Valid() bool {
	_, ok := transitions()[s]
	return ok
}

// Open reports whether a deal in this stage is still in progress (not won or lost).
func (s Stage) Open() bool {
	return s != StageWon && s != StageLost
}
