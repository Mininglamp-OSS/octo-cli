package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestGetSkill_SearchBoundsAndCancellation(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	res := s.getSkill(context.Background(), "search", "", strings.Repeat("message ", 17), "", "")
	if toolText(t, res)["truncated"] != true {
		t.Fatal("get_skill must retain shared search bounds")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertSearchCancelled(t, s.getSkill(ctx, "search", "", "message", "", ""))
}

func TestExecute_MultipartDryRunUploadPolicy(t *testing.T) {
	for _, httpMode := range []bool{false, true} {
		s := facadeServer(t, facadeTwo)
		s.httpMode = httpMode
		args := map[string]any{"operation_id": "file.upload", "dry_run": true,
			"arguments": map[string]any{"file_path": "missing.bin"}}
		res := callTool(t, s, "execute", args)
		if toolText(t, res)["status"] != "validation_error" {
			t.Fatal("default-deny upload policy must apply to dry-run on both transports")
		}
		// Even the root's parents are absent: planning must not stat/readlink them.
		s.uploadRoot = t.TempDir() + "/absent-parent/absent-root"
		res = callTool(t, s, "execute", args)
		if res.IsError {
			t.Fatalf("lexical dry-run must not access the filesystem: %s", res.Content[0].Text)
		}
		args["arguments"] = map[string]any{"file_path": "../outside.bin"}
		if res = callTool(t, s, "execute", args); !res.IsError {
			t.Fatal("dry-run must reject lexical traversal")
		}
	}
}

func TestExecute_OperatorDryRunAndForcedAudit(t *testing.T) {
	be := &backend{}
	backend := be.server(t)
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	t.Setenv("OCTO_TOKEN", "bf_test")
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
	s := facadeServer(t, facadeTwo)
	s.baseGlobals = cmdutil.GlobalOptions{DryRun: true}
	s.trusted = TrustedContext{ChannelID: "forced-channel"}
	res := callTool(t, s, "execute", map[string]any{"operation_id": "message.send",
		"arguments": map[string]any{"channel_id": "model-channel", "channel_type": 1, "payload": map[string]any{"text": "x"}}})
	out := toolText(t, res)
	if res.IsError || out["dry_run"] != true || be.path != "" {
		t.Fatalf("operator dry-run must preview without a request: %s", res.Content[0].Text)
	}
	env := out["envelope"].(map[string]any)
	if !strings.Contains(res.Content[0].Text, "forced-channel") || env["_forced_arguments"] == nil {
		t.Fatal("execute must retain trusted projection and the shared audit trail")
	}
	s.allowLocalUpload = true
	res = callTool(t, s, "execute", map[string]any{"operation_id": "file.upload",
		"arguments": map[string]any{"file_path": "/absent/never-read.bin"}})
	if res.IsError || toolText(t, res)["dry_run"] != true || be.path != "" {
		t.Fatalf("operator dry-run must use metadata-only multipart planning: %s", res.Content[0].Text)
	}
}
