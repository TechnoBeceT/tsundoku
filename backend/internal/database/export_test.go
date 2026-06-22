// Package database — test-only exports for white-box testing.
// This file is compiled only when running tests (it belongs to package
// "database", not "database_test") and exposes internal seams without
// polluting the production API.
package database

import (
	"context"
	"database/sql"
)

// SetPingForTest replaces the package-level pingDB function with fn and
// returns a restore function that puts the original back.  Callers must
// defer the restore to avoid state leaking between tests.
//
//	restore := SetPingForTest(myFakePing)
//	defer restore()
func SetPingForTest(fn func(context.Context, *sql.DB) error) (restore func()) {
	orig := pingDB
	pingDB = fn
	return func() { pingDB = orig }
}

// MaxAttempts exposes retryPolicy.maxAttempts so tests can assert that the
// retry loop ran the full budget without hard-coding the constant.
var MaxAttempts = &retryPolicy.maxAttempts
