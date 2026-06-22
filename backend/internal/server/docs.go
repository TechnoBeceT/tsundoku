package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/api"
)

// scalarHTML is the Scalar API reference UI page.
// It loads the spec from /docs/openapi.yaml via the CDN build of Scalar so
// no npm step is required for the backend binary.
//
// The script tag pins @scalar/api-reference@1.60.0 with a SHA-384 Subresource
// Integrity hash so any CDN-side tampering is detected and rejected by the
// browser before execution.
const scalarHTML = `<!doctype html>
<html>
  <head>
    <title>Tsundoku API</title>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
  </head>
  <body>
    <script id="api-reference" data-url="/docs/openapi.yaml"></script>
    <script
      src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.60.0/dist/browser/standalone.js"
      integrity="sha384-3sxnxyp7pbU2/o4+gs4EbvQ4YKyF60pWDL2LW8SoFZNQBTSiPah2xcHpxsndZEgF"
      crossorigin="anonymous"></script>
  </body>
</html>
`

// RegisterDocs attaches the /docs and /docs/openapi.yaml routes to e.
//
// GET /docs serves the Scalar interactive API reference UI.
// GET /docs/openapi.yaml serves the raw OpenAPI 3.1 contract so tools and
// the frontend codegen can consume it directly.
func RegisterDocs(e *echo.Echo) {
	e.GET("/docs", serveDocs)
	e.GET("/docs/openapi.yaml", serveSpec)
}

// serveDocs renders the Scalar HTML page.
func serveDocs(c echo.Context) error {
	return c.HTML(http.StatusOK, scalarHTML)
}

// serveSpec returns the raw OpenAPI 3.1 YAML that was embedded at build time.
func serveSpec(c echo.Context) error {
	return c.Blob(http.StatusOK, "application/yaml", api.Spec)
}
