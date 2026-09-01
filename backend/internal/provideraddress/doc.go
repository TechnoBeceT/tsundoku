// Package provideraddress owns the monotonic persistence rule for a live
// provider's engine address mode. A resolved mode may promote an unknown row,
// but no observation may downgrade or replace a mode that is already known.
package provideraddress
