package e2e

import (
	"testing"
)

func TestE2EListenerPortsDistinct(t *testing.T) {
	// Repeating this check guards against a regression to two independent
	// free-port allocations, which can hand back the same ephemeral port.
	for i := 0; i < 64; i++ {
		playerPort, adminPort := freeListenerPorts(t)
		if playerPort <= 0 || adminPort <= 0 || playerPort == adminPort {
			t.Fatalf("iteration %d: invalid or identical listener ports: player=%d admin=%d", i, playerPort, adminPort)
		}
		env := baseEnv(t, "unused-test.db")
		if env["GB_ADDR"] == env["GB_ADMIN_ADDR"] {
			t.Fatalf("iteration %d: baseEnv assigned both listeners %q", i, env["GB_ADDR"])
		}
	}
}
