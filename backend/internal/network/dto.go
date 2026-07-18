package network

import (
	"strconv"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
)

// EndpointDTO is the wire shape for one network endpoint. It carries BOTH
// field-groups (the SOCKS group and the FlareSolverr group); the kind field
// tells the caller which group is meaningful for this row.
//
// SECURITY: the SOCKS password is DELIBERATELY ABSENT — it is write-only (set
// via create/update, never echoed back), mirroring how the engine config never
// returns SOCKS/FlareSolverr secrets.
type EndpointDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`

	// SOCKS field-group (meaningful when kind == "socks").
	Host         string `json:"host"`
	Port         int    `json:"port"`
	SocksVersion int    `json:"socksVersion"`
	Username     string `json:"username"`
	// Password is intentionally omitted — write-only.

	// FlareSolverr field-group (meaningful when kind == "flaresolverr").
	URL        string `json:"url"`
	FSProxy    string `json:"fsProxy"`
	Session    string `json:"session"`
	SessionTTL int    `json:"sessionTtl"`
	Timeout    int    `json:"timeout"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// newEndpointDTO maps an ent.NetworkEndpoint into its wire shape, dropping the
// password (write-only).
func newEndpointDTO(e *ent.NetworkEndpoint) EndpointDTO {
	return EndpointDTO{
		ID:           e.ID.String(),
		Name:         e.Name,
		Kind:         e.Kind,
		Enabled:      e.Enabled,
		Host:         e.Host,
		Port:         e.Port,
		SocksVersion: e.SocksVersion,
		Username:     e.Username,
		URL:          e.URL,
		FSProxy:      e.FsProxy,
		Session:      e.Session,
		SessionTTL:   e.SessionTTL,
		Timeout:      e.Timeout,
		CreatedAt:    e.CreatedAt,
		UpdatedAt:    e.UpdatedAt,
	}
}

// BindingDTO is the wire shape for one per-source network binding. sourceId is
// stringified (a 64-bit source id can exceed JS's safe-integer range, matching
// the rest of the API). The two endpoint ids are nullable — a null means "no
// per-source override for that dimension" (direct/global).
type BindingDTO struct {
	SourceID        string  `json:"sourceId"`
	SocksEndpointID *string `json:"socksEndpointId"`
	FlareMode       string  `json:"flareMode"`
	FlareEndpointID *string `json:"flareEndpointId"`
}

// newBindingDTO maps an ent.SourceNetworkBinding into its wire shape.
func newBindingDTO(b *ent.SourceNetworkBinding) BindingDTO {
	return BindingDTO{
		SourceID:        strconv.FormatInt(b.SourceID, 10),
		SocksEndpointID: uuidPtrToStringPtr(b.SocksEndpointID),
		FlareMode:       b.FlareMode,
		FlareEndpointID: uuidPtrToStringPtr(b.FlareEndpointID),
	}
}
