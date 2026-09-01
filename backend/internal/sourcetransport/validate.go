package sourcetransport

import (
	"errors"
	"fmt"

	"github.com/technobecet/tsundoku/internal/runtimepolicy"
)

// ErrInvalidPolicy identifies an unsupported patch operation or transport mode.
var ErrInvalidPolicy = errors.New("invalid source transport policy")

func validatePatch(patch Patch) error {
	if err := validateOperation("reuse bypass session", patch.ReuseBypassSession.Operation); err != nil {
		return err
	}
	if err := validateOperation("image connection mode", patch.ImageConnectionMode.Operation); err != nil {
		return err
	}
	if err := validateOperation("embedded browser", patch.KCEFPolicy.Operation); err != nil {
		return err
	}
	if patch.ImageConnectionMode.Operation == PatchSet && !validImageConnectionMode(patch.ImageConnectionMode.Value) {
		return fmt.Errorf("%w: image connection mode must be fresh or reuse", ErrInvalidPolicy)
	}
	if patch.KCEFPolicy.Operation == PatchSet && !validKCEFPolicy(patch.KCEFPolicy.Value) {
		return fmt.Errorf("%w: embedded browser policy must be auto, required, or disabled", ErrInvalidPolicy)
	}
	return nil
}

func validKCEFPolicy(policy runtimepolicy.KCEFPolicy) bool {
	return policy == runtimepolicy.KCEFPolicyAuto || policy == runtimepolicy.KCEFPolicyRequired || policy == runtimepolicy.KCEFPolicyDisabled
}

// Validate checks a patch without mutating storage.
func Validate(patch Patch) error { return validatePatch(patch) }

func validateOperation(field string, operation PatchOperation) error {
	if operation > PatchClear {
		return fmt.Errorf("%w: unsupported %s operation %d", ErrInvalidPolicy, field, operation)
	}
	return nil
}

func validImageConnectionMode(mode ImageConnectionMode) bool {
	return mode == ImageConnectionFresh || mode == ImageConnectionReuse
}
