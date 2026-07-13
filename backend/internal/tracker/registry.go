package tracker

// Registry holds an ORDERED, ID-INDEXED set of Trackers. Unlike
// internal/metadata.Registry (which is Key()-scanned, since the metadata
// engine always fans a query out to every registered provider), the tracker
// subsystem's HTTP surface addresses one tracker at a time by its numeric
// registry id (GET /api/trackers/:id/..., spec §4) — so lookup here is a
// map, not a scan.
//
// Registry itself stays ENT-FREE like the rest of this package; the
// concrete wiring (which Trackers, built with which client-id config) lives
// in internal/tracker/providers, mirroring internal/metadata/providers'
// same cycle-avoidance shape.
type Registry struct {
	byID  map[int]Tracker
	order []int
}

// NewRegistry builds a Registry over trackers, preserving call order for
// Trackers() while indexing every tracker by its ID() for ByID lookups. A
// later tracker in the call list with a duplicate ID() OVERWRITES an
// earlier one in the id index (last write wins) — callers are expected to
// pass exactly one Tracker per registry id (see internal/tracker/providers,
// which asserts this).
func NewRegistry(trackers ...Tracker) *Registry {
	r := &Registry{byID: make(map[int]Tracker, len(trackers))}
	for _, t := range trackers {
		r.byID[t.ID()] = t
		r.order = append(r.order, t.ID())
	}
	return r
}

// Trackers returns the registered Trackers in registration order.
func (r *Registry) Trackers() []Tracker {
	out := make([]Tracker, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

// ByID looks up a Tracker by its numeric registry id (one of the ID*
// constants in tracker.go). The second return value reports whether one is
// registered.
func (r *Registry) ByID(id int) (Tracker, bool) {
	t, ok := r.byID[id]
	return t, ok
}
