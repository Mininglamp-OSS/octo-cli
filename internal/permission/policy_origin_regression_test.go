package permission

import "testing"

func TestPolicyOriginNeedsIndependentHostAllowlist(t *testing.T) {
	for _, origin := range []string{"https://169.254.169.254", "https://127.0.0.1", "https://unconfigured.example"} {
		if _, err := NewSelfPolicyReader(origin, "bot", "bf_synthetic", nil); err == nil {
			t.Fatalf("untrusted default origin accepted: %s", origin)
		}
	}
}
