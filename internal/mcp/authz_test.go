package mcp

import "testing"

func TestTrustedContext_OverridesWhitelistedArgs(t *testing.T) {
	tc := TrustedContext{ChannelID: "trusted-chan", ChannelType: "1", OnBehalfOf: "u-real"}
	args := map[string]any{"channel_id": "attacker-chan", "payload": map[string]any{"text": "hi"}}
	forced := tc.apply("message.send", args)

	if args["channel_id"] != "trusted-chan" {
		t.Errorf("channel_id = %v, want the forced trusted value", args["channel_id"])
	}
	if args["channel_type"] != "1" {
		t.Errorf("channel_type = %v, want forced 1", args["channel_type"])
	}
	if args["on_behalf_of"] != "u-real" {
		t.Errorf("on_behalf_of = %v, want forced u-real", args["on_behalf_of"])
	}
	if !forced["channel_id"] {
		t.Errorf("channel_id should be reported as forced")
	}
	// A field the model set that is not sensitive stays untouched.
	if _, ok := args["payload"]; !ok {
		t.Errorf("non-sensitive payload must be preserved")
	}
}

// TestTrustedContext_SameNameParamNotForcedOnOtherOps is the anti-footgun guard
// : drive's space_id is a drive RESOURCE id, not an Octo space
// context. Because the white-list is per-operation, a trusted SpaceID must NOT
// rewrite drive's same-named argument.
func TestTrustedContext_SameNameParamNotForcedOnOtherOps(t *testing.T) {
	tc := TrustedContext{SpaceID: "octo-space-x"}

	driveArgs := map[string]any{"space_id": "shared:abc-uuid"}
	tc.apply("drive.file.get", driveArgs)
	if driveArgs["space_id"] != "shared:abc-uuid" {
		t.Errorf("drive space_id must NOT be overridden by trusted Octo space, got %v", driveArgs["space_id"])
	}

	// message.search's channel_id is optional cross-channel scope: with no
	// configured channel value the empty-skip leaves it model-controlled (the
	// documented default). When the operator DOES configure a channel, the
	// search family is force-when-configured — pinned by
	// TestAuthzCoverage_ConfiguredForceReachesEverySessionBoundOp.
	searchArgs := map[string]any{"channel_id": "chan-explicit"}
	tc.apply("message.search", searchArgs)
	if searchArgs["channel_id"] != "chan-explicit" {
		t.Errorf("message.search channel_id must not be forced, got %v", searchArgs["channel_id"])
	}

	// But bot.space-members' space_id IS a cross-space override channel and must be forced.
	botArgs := map[string]any{"space_id": "other-space"}
	tc.apply("bot.space-members", botArgs)
	if botArgs["space_id"] != "octo-space-x" {
		t.Errorf("bot.space-members space_id should be forced to the trusted space, got %v", botArgs["space_id"])
	}
}

func TestTrustedContext_ServerManagedReflectsConfiguredValues(t *testing.T) {
	empty := TrustedContext{}
	if len(empty.serverManaged("message.send")) != 0 {
		t.Errorf("empty context manages nothing")
	}
	tc := TrustedContext{ChannelID: "c"}
	managed := tc.serverManaged("message.send")
	if !managed["channel_id"] {
		t.Errorf("channel_id should be server-managed when a trusted value is set")
	}
	if managed["channel_type"] {
		t.Errorf("channel_type has no trusted value set, must not be reported managed")
	}
}
