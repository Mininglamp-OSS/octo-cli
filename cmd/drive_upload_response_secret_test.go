package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// The composite upload command does not go through the generated command
// runner, so it must pass response-secret metadata to the transport itself.
// This exercises the prepare request that created the metadata gap before the
// caller-passed design: a failure body naming upload_url must never reach the
// structured error or rendered stderr.
func TestDriveUploadFile_PrepareErrorRedactsResponseSecret(t *testing.T) {
	const signedURL = "https://objects.example.test/prepare?signature=composite-test-only"

	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w,
			`{"error":"invalid_argument","message":"prepare rejected %s","details":{"upload_url":%q}}`,
			signedURL, signedURL)
	}, nil)

	src := filepath.Join(t.TempDir(), "large.bin")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := env.run("drive", "upload", "file", src, "--space-id", "shared:s1")
	if err == nil {
		t.Fatal("expected the composite prepare request to fail")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected a structured error, got %v", err)
	}
	visible := strings.Join([]string{err.Error(), string(ee.Detail), env.tf.ErrOut.String()}, "\n")
	if strings.Contains(visible, signedURL) {
		t.Fatalf("composite upload error exposed the prepare upload_url:\n%s", visible)
	}
	if !strings.Contains(visible, "REDACTED") {
		t.Fatalf("composite upload error did not show the response secret was masked:\n%s", visible)
	}
}

func TestDriveUploadFile_ResponseSecretMetadataForCallerRequests(t *testing.T) {
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, nil)

	for _, operationID := range []string{
		"drive.upload.prepare",
		"drive.upload.confirm",
		"drive.upload.cancel",
	} {
		t.Run(operationID, func(t *testing.T) {
			service, fields, err := responseSecretMetadataForOperation(env.tf.Factory, operationID)
			if err != nil {
				t.Fatalf("response secret metadata: %v", err)
			}
			if service != "drive" {
				t.Errorf("service = %q, want drive", service)
			}
			if operationID == "drive.upload.prepare" {
				if len(fields) != 1 || fields[0] != "upload_url" {
					t.Errorf("prepare response secret fields = %v, want [upload_url]", fields)
				}
			}
		})
	}
}
