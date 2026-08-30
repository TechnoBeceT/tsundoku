package network

import (
	"time"

	configurationhandler "github.com/technobecet/tsundoku/internal/handler/sourceconfiguration"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

type runtimeStatusDTO struct {
	Status           string     `json:"status"`
	DesiredRevision  int64      `json:"desiredRevision"`
	AppliedRevision  int64      `json:"appliedRevision"`
	LastApplyAttempt *time.Time `json:"lastApplyAttempt"`
	LastApplyError   string     `json:"lastApplyError"`
}

type mutationResponse struct {
	Configuration configurationhandler.ConfigurationDTO `json:"configuration"`
	Runtime       runtimeStatusDTO                      `json:"runtime"`
}

func newMutationResponse(configuration configurationhandler.ConfigurationDTO, intent sourcetransport.Intent) mutationResponse {
	status := "pending"
	if intent.DesiredRevision <= intent.AppliedRevision {
		status = "applied"
	}
	return mutationResponse{
		Configuration: configuration,
		Runtime: runtimeStatusDTO{
			Status: status, DesiredRevision: intent.DesiredRevision, AppliedRevision: intent.AppliedRevision,
			LastApplyAttempt: intent.LastApplyAttempt, LastApplyError: intent.LastApplyError,
		},
	}
}
