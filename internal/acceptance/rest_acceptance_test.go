//go:build acceptance && admin

package acceptance

import (
	"os"
	"testing"
)

func TestRESTAcceptanceAdminLifecycle(t *testing.T) {
	requireAcceptance(t)
	if os.Getenv("MOTHERDUCK_ADMIN_TOKEN") == "" {
		t.Fatal("MOTHERDUCK_ADMIN_TOKEN is required for MotherDuck REST acceptance tests")
	}

	runScript(t, "scripts/test-live-rest-token-matrix.sh")
}
