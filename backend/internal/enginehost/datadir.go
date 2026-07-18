package enginehost

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// fsSafeKeyLen is how many hex chars of the profile-key hash name a data dir.
// 16 hex chars = 64 bits of the SHA-256 — astronomically collision-safe for the
// handful of profiles a single owner ever runs, and short enough to keep the
// path tidy.
const fsSafeKeyLen = 16

// fsSafeKey turns a profile Key into a deterministic, filesystem-safe directory
// name. A Key is a composite of endpoint UUIDs joined by "|" (see
// engineroute.profileKey) — the "|" and any other awkward characters must never
// reach a filesystem path, so we hash it rather than sanitize it. The hash is
// deterministic, so the same profile always maps to the same data dir across
// restarts (its extensions working-set + single-instance lock persist).
func fsSafeKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:fsSafeKeyLen]
}

// dataDirFor computes a profile's own engine-host data root:
// "<base>/profiles/<fsSafeKey>". Each non-default instance gets its own dir so
// its single-instance file lock and extensions working-set never collide with
// another instance's (or the default instance's, which lives directly at base).
func dataDirFor(base, key string) string {
	return filepath.Join(base, "profiles", fsSafeKey(key))
}
