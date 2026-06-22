// Package config — test-only exports. This file is compiled only during
// `go test`; nothing in it is visible in the production binary.
package config

// ExportValidateForTest exposes the unexported validate method so that
// black-box tests in package config_test can exercise the fail-closed path
// without importing os or bypassing the Load() entrypoint.
func ExportValidateForTest(c *Config) error {
	return c.validate()
}
