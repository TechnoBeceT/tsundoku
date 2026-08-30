// Package sourceconfiguration composes the installed-source catalog with
// global settings and the independently-owned per-source policy stores. Its
// routing view keeps persisted binding intent distinct from effective routing
// after endpoint availability is resolved. Reads are bounded bulk snapshots
// joined in memory by numeric source ID; no source count can turn the overview
// into per-source storage or engine fan-out.
package sourceconfiguration
