// Package api embeds the OpenAPI contract, the source of truth for /api/v1.
package api

import _ "embed"

// OpenAPI is api/openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte
