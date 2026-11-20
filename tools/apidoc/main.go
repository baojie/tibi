// Command apidoc writes docs/API.md from api/openapi.yaml:
//
//	go run ./tools/apidoc > docs/API.md
//	go run ./tools/apidoc -check   # exit 1 if docs/API.md is stale
package main

import (
	"flag"
	"fmt"
	"os"

	"tibi/api"
	"tibi/internal/apispec"
)

func main() {
	check := flag.Bool("check", false, "compare with docs/API.md instead of printing")
	flag.Parse()
	s, err := apispec.Load(api.OpenAPI)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	md := s.Markdown()
	if *check {
		cur, err := os.ReadFile("docs/API.md")
		if err != nil || string(cur) != md {
			fmt.Fprintln(os.Stderr, "docs/API.md is out of date: go run ./tools/apidoc > docs/API.md")
			os.Exit(1)
		}
		return
	}
	fmt.Print(md)
}
