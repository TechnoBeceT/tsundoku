package sourcethroughput

import (
	"errors"
	"fmt"
	"time"
)

const (
	minDownloadConcurrency = 1
	maxDownloadConcurrency = 32
)

// ErrInvalidPolicy identifies a patch containing an unsupported operation or
// an override outside the runtime setting's accepted range.
var ErrInvalidPolicy = errors.New("invalid source throughput policy")

func validatePatch(patch Patch) error {
	if err := validateOperation("download concurrency", patch.DownloadConcurrency.Operation); err != nil {
		return err
	}
	if err := validateOperation("image request delay", patch.ImageRequestDelay.Operation); err != nil {
		return err
	}
	if patch.DownloadConcurrency.Operation == PatchSet &&
		(patch.DownloadConcurrency.Value < minDownloadConcurrency || patch.DownloadConcurrency.Value > maxDownloadConcurrency) {
		return fmt.Errorf("%w: download concurrency must be between %d and %d", ErrInvalidPolicy, minDownloadConcurrency, maxDownloadConcurrency)
	}
	if patch.ImageRequestDelay.Operation == PatchSet && patch.ImageRequestDelay.Value < 0 {
		return fmt.Errorf("%w: image request delay must not be negative", ErrInvalidPolicy)
	}
	if patch.ImageRequestDelay.Operation == PatchSet && patch.ImageRequestDelay.Value%time.Millisecond != 0 {
		return fmt.Errorf("%w: image request delay must be a whole number of milliseconds", ErrInvalidPolicy)
	}
	return nil
}

// Validate checks a patch without mutating storage. HTTP and other adapters use
// it to reject invalid inputs before invoking Update while sharing one range
// and duration-precision contract with the service boundary.
func Validate(patch Patch) error { return validatePatch(patch) }

func validateOperation(field string, operation PatchOperation) error {
	if operation > PatchClear {
		return fmt.Errorf("%w: unsupported %s operation %d", ErrInvalidPolicy, field, operation)
	}
	return nil
}
