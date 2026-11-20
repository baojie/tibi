// Command apidoc writes docs/API.md from api/openapi.yaml:
//
//	go run ./tools/apidoc > docs/API.md
package main

import (
	"fmt"
	"os"

	"tibi/api"
	"tibi/internal/apispec"
)

func main() {
	s, err := apispec.Load(api.OpenAPI)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(s.Markdown())
}
