package sourcetransport

import (
	"errors"
	"fmt"
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
	if patch.ImageConnectionMode.Operation == PatchSet && !validImageConnectionMode(patch.ImageConnectionMode.Value) {
		return fmt.Errorf("%w: image connection mode must be fresh or reuse", ErrInvalidPolicy)
	}
	return nil
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
