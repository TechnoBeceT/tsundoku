// Command tsundoku is the Tsundoku backend server.
// It starts an Echo HTTP server on the configured port and serves the
// Tsundoku API and static SPA assets.
//
// TODO(task-9): replace the literal port with config.Load() once the config
// package is implemented in Task 2.
package main

import (
	"log"

	"github.com/technobecet/tsundoku/internal/server"
)

// port is the address the server listens on.
// Replaced by cfg.Server.Port in Task 9.
const port = ":9833"

func main() {
	e := server.New()
	log.Printf("tsundoku: listening on %s", port)
	if err := e.Start(port); err != nil {
		log.Fatalf("tsundoku: server error: %v", err)
	}
}
