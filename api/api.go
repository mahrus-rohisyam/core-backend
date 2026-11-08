// Package api holds the OpenAPI description of the core modules' endpoints. The frontend
// generates its TypeScript types from it; apps merge it with the description of their own routes.
package api

import _ "embed"

// Spec is openapi.json.
//
//go:embed openapi.json
var Spec []byte
