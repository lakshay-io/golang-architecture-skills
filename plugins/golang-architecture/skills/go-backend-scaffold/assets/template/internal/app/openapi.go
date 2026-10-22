package app

import (
	"github.com/gin-gonic/gin"

	"example.com/goservice/internal/api"
	"example.com/goservice/internal/api/apidoc"
)

// OpenAPI renders the service's OpenAPI document: the content of
// docs/openapi.json, which `make openapi` writes. It needs no configuration
// and opens no connection.
func OpenAPI() ([]byte, error) {
	gin.SetMode(gin.ReleaseMode) // no route listing on stdout
	return api.Spec(apiInfo(), routes(services{}))
}

// apiInfo describes the service in its OpenAPI document. Add to Servers the
// base URLs clients call (never production's: the docs portal's try-it calls
// them), and to Security the token schemes the routes use:
//
//	Servers:  {URL: "https://goservice-staging.example.com", Description: "Staging"},
//	Security: map[string]apidoc.Scheme{"userToken": {Description: "…", Errors: []apidoc.Error{…}}},
func apiInfo() apidoc.Info {
	return apidoc.Info{
		Title:       "goservice",
		Version:     "v1",
		Description: "The goservice API. Every response, success or failure, has the same body: success, status, code, message, data or details, and requestId.",
		Servers: []apidoc.Server{
			{URL: "http://localhost:8080", Description: "Local: make run"},
		},
	}
}
