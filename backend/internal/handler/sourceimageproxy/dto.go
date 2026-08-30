package sourceimageproxy

import (
	"time"

	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

type runtimeStatusDTO struct {
	Status           string     `json:"status"`
	DesiredRevision  int64      `json:"desiredRevision"`
	AppliedRevision  int64      `json:"appliedRevision"`
	LastApplyAttempt *time.Time `json:"lastApplyAttempt"`
	LastApplyError   string     `json:"lastApplyError"`
}

type mutationResponse[C any] struct {
	Configuration C                `json:"configuration"`
	Runtime       runtimeStatusDTO `json:"runtime"`
}

func newMutationResponse[C any](configuration C, intent sourcetransport.Intent) mutationResponse[C] {
	status := "pending"
	if intent.DesiredRevision <= intent.AppliedRevision {
		status = "applied"
	}
	return mutationResponse[C]{
		Configuration: configuration,
		Runtime: runtimeStatusDTO{
			Status:           status,
			DesiredRevision:  intent.DesiredRevision,
			AppliedRevision:  intent.AppliedRevision,
			LastApplyAttempt: intent.LastApplyAttempt,
			LastApplyError:   intent.LastApplyError,
		},
	}
}
