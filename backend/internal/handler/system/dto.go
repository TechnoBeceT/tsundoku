// Package system contains the HTTP handler for the GET /api/system endpoint,
// which surfaces read-only env-structural information for the Settings pane.
package system

// SystemDTO is the response body for GET /api/system.
// It contains only credential-free display values; database passwords and
// usernames are intentionally excluded.
type SystemDTO struct {
	// StorageFolder is the absolute path to the manga library on disk.
	StorageFolder string `json:"storageFolder"`
	// ServerPort is the TCP port the HTTP server listens on.
	ServerPort string `json:"serverPort"`
	// Database is a credential-free display string for the PostgreSQL target:
	// "host:port/name". Password and username are never included.
	Database string `json:"database"`
}
