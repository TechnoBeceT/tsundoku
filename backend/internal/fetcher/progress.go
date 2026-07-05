package fetcher

import "context"

// ProgressFunc is a per-page progress sink: a fetcher implementation calls it
// once after each page is fetched with the running count (current) and the total
// number of pages in the chapter. current runs 1..total; a nil-safe no-op is
// substituted when no sink is set (see ProgressFrom), so implementations may call
// the resolved sink unconditionally.
type ProgressFunc func(current, total int)

// progressKey is the unexported context key under which a ProgressFunc is stored.
// A dedicated zero-size struct type guarantees the key can never collide with a
// key from another package.
type progressKey struct{}

// WithProgress returns a copy of ctx carrying fn as its progress sink.
//
// The sink rides on the context — rather than being added to the
// ChapterFetcher.Fetch signature — SO THAT the port stays frozen: every existing
// fetcher fake keeps compiling untouched, and progress is a purely optional,
// caller-supplied concern that a fetcher may honour or ignore. The dispatcher sets
// the sink per chapter just before it calls Fetch; the suwayomi fetcher resolves it
// with ProgressFrom and drives it from its page loop.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// ProgressFrom resolves the ProgressFunc carried by ctx. It NEVER returns nil:
// when no sink was set (or a nil one was stored) it returns a no-op, so callers
// can invoke the result directly without a nil guard.
func ProgressFrom(ctx context.Context) ProgressFunc {
	if fn, ok := ctx.Value(progressKey{}).(ProgressFunc); ok && fn != nil {
		return fn
	}
	return func(int, int) {}
}
