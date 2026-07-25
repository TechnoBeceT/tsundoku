package enginehost

// Rerouter is how the launcher + supervisor move a down profile's sources between
// its own engine-host instance and the DEFAULT instance (GAP-114). It is the ONE
// seam between the OS-process side (this package) and the routing table
// (engineroute.Router) so enginehost never has to own the routing map:
//   - Degrade force-routes the given sources to the default instance while their
//     profile's instance is observed down (a meaningful error against the default
//     instead of connection-refused to a dead port).
//   - Restore returns them to their base route once the instance is healthy again
//     (or the profile is retired).
//
// *engineroute.Router satisfies it via its degrade-overlay methods. It is
// OPTIONAL: a launcher constructed without WithRerouter holds a nil Rerouter and
// every degrade/restore is a pure no-op, so a deployment (or test) with no
// per-source routing behaves exactly as before.
type Rerouter interface {
	// Degrade force-routes sourceIDs to the default instance.
	Degrade(sourceIDs []int64)
	// Restore clears sourceIDs from the degrade overlay (idempotent).
	Restore(sourceIDs []int64)
}
