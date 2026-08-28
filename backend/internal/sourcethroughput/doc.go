// Package sourcethroughput persists optional per-source download-concurrency
// and image-request-delay overrides and resolves them against runtime defaults.
//
// A missing override inherits its global setting. An explicit zero image delay
// is distinct from absence and disables image pacing for that source. Clearing
// the last override removes the row, so complete inheritance has one canonical
// representation: no stored policy row. Image delays are stored in milliseconds;
// positive values that are not a whole number of milliseconds are rejected.
package sourcethroughput
