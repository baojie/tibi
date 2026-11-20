package server_test

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/baojie/tibi/internal/testutil"
)

// TestMain checks, after a full run, that the suite exercised every response
// documented in api/openapi.yaml (each one was also validated against its
// schema as it happened; see testutil.contractChecked).
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 && flag.Lookup("test.run").Value.String() == "" {
		missing := testutil.UncoveredResponses(append(testutil.TusOperations,
			"GET /healthz 503", // needs a broken database
		)...)
		if len(missing) > 0 {
			fmt.Fprintln(os.Stderr, "API contract: documented responses never exercised by the tests:")
			for _, m := range missing {
				fmt.Fprintln(os.Stderr, "  ", m)
			}
			code = 1
		}
	}
	os.Exit(code)
}
