package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// swaggerUIVersion pins the Swagger UI assets loaded from the CDN.
const swaggerUIVersion = "5.17.14"

const swaggerHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Mini CRM API</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@` + swaggerUIVersion + `/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@` + swaggerUIVersion + `/swagger-ui-bundle.js"></script>
  <script>
    // Same origin as the API, so "Try it out" sends the auth cookies set by /auth/login.
    SwaggerUIBundle({ url: "/docs/openapi.yaml", dom_id: "#swagger-ui" });
  </script>
</body>
</html>`

// MountDocs serves Swagger UI at /docs and the spec at /docs/openapi.yaml.
// Call it only in development (NFR-QA-04). specPath is read from disk on each
// request, so edits to the spec show up on reload without restarting.
func MountDocs(r *gin.Engine, specPath string) {
	r.GET("/docs", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(swaggerHTML))
	})
	r.GET("/docs/openapi.yaml", func(c *gin.Context) {
		c.Header("Content-Type", "application/yaml")
		c.File(specPath)
	})
}
