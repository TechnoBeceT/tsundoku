// Package sourceimageproxy owns atomic per-source membership in the global
// image-proxy allowlist. It validates identity against the live engine catalog,
// then commits the locked membership change and source runtime intent in one
// transaction so concurrent source edits cannot lose one another.
package sourceimageproxy
