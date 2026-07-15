// Package sourceengine — test-only exports.
//
// This file is compiled only during `go test`; nothing in it is visible in the
// production binary.
package sourceengine

// ValidateImagePage exposes the unexported validateImagePage guard so black-box
// tests can pin the decode/content/empty/oversize checks with real image bytes.
func ValidateImagePage(data []byte) error {
	return validateImagePage(data)
}
