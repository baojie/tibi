// Command apidoc writes docs/API.md from api/openapi.yaml:
//
//	go run ./tools/apidoc > docs/API.md
//	go run ./tools/apidoc -check   # exit 1 if docs/API.md or the JS types are stale
//	go run ./tools/apidoc -js > extensions/chrome/lib/api.gen.js
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/baojie/tibi/api"
	"github.com/baojie/tibi/internal/apispec"
)

func main() {
	check := flag.Bool("check", false, "compare with docs/API.md and the JS types instead of printing")
	js := flag.Bool("js", false, "print JSDoc types for JavaScript clients instead of Markdown")
	flag.Parse()
	s, err := apispec.Load(api.OpenAPI)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *check {
		for file, want := range map[string]string{"docs/API.md": s.Markdown(), "extensions/chrome/lib/api.gen.js": s.JS()} {
			cur, err := os.ReadFile(file)
			if err != nil || string(cur) != want {
				fmt.Fprintf(os.Stderr, "%s is out of date: go run ./tools/apidoc [-js] > %s\n", file, file)
				os.Exit(1)
			}
		}
		return
	}
	if *js {
		fmt.Print(s.JS())
		return
	}
	fmt.Print(s.Markdown())
}
