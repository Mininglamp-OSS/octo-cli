package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func rootWithDriveURL(t *testing.T, baseURL string) (*cobra.Command, *cmdutil.TestFactory) {
	t.Helper()
	const token = "bf_bot"
	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: baseURL, BotToken: token, Format: "json"}
	tf.SetConfig(cfg)
	cred := &credential.BotCredential{Token: token, Source: "test"}
	tf.SetCredential(cred)
	tf.SetClient(client.New(cfg, cred, client.Options{ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	return root, tf
}

func TestDrive_MultipartRequestBoundsRejectedBeforeBackend(t *testing.T) {
	tooManyParts := make([]int, 21)
	for i := range tooManyParts {
		tooManyParts[i] = i + 1
	}
	partsPresignBody, err := json.Marshal(map[string]any{"part_numbers": tooManyParts})
	if err != nil {
		t.Fatal(err)
	}
	completeBody := func(partNumber int) string {
		body, marshalErr := json.Marshal(map[string]any{
			"parts": []map[string]any{{"part_number": partNumber, "etag": "etag-1"}},
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return string(body)
	}

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "parts-presign rejects more than 20 part numbers",
			args: []string{"drive", "upload", "parts-presign", "42", "--upload-id", "upload-1", "--data", string(partsPresignBody)},
		},
		{
			name: "parts-presign rejects part number zero",
			args: []string{"drive", "upload", "parts-presign", "42", "--upload-id", "upload-1", "--data", `{"part_numbers":[0]}`},
		},
		{
			name: "parts-presign rejects part number above 10000",
			args: []string{"drive", "upload", "parts-presign", "42", "--upload-id", "upload-1", "--data", `{"part_numbers":[10001]}`},
		},
		{
			name: "complete rejects part number zero",
			args: []string{"drive", "upload", "complete", "42", "--upload-id", "upload-1", "--data", completeBody(0)},
		},
		{
			name: "complete rejects part number above 10000",
			args: []string{"drive", "upload", "complete", "42", "--upload-id", "upload-1", "--data", completeBody(10001)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			})
			root.SetArgs(tc.args)

			err := root.Execute()
			if err == nil {
				t.Fatal("expected local request validation to reject the multipart bounds violation")
			}
			ee := output.AsExitError(err)
			if ee == nil || ee.Type != "validation" || ee.Code != "VALIDATION_ERROR" {
				t.Fatalf("error = %#v, want validation / VALIDATION_ERROR", err)
			}
			if hits.Load() != 0 {
				t.Fatalf("backend hits = %d, want 0; invalid multipart bounds must fail before HTTP", hits.Load())
			}
		})
	}
}

func TestDrive_PartsPresignURLIsDeclaredResponseSecret(t *testing.T) {
	op, ok := registry.MustNew().GetOperation("drive.upload.parts_presign")
	if !ok || op.ResponseSchema == nil {
		t.Fatal("drive.upload.parts_presign response schema not found")
	}
	parts, ok := op.ResponseSchema.Properties["parts"]
	if !ok || parts.Items == nil {
		t.Fatal("drive.upload.parts_presign parts item schema not found")
	}
	url, ok := parts.Items.Properties["url"]
	if !ok || !url.Secret {
		t.Fatal("drive.upload.parts_presign parts[].url must be x-octo-secret")
	}
	if got, want := op.ResponseSecretFields, []string{"parts[].url"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("response secret fields = %v, want %v", got, want)
	}
}

func TestDrive_PartsPresignErrorRedactsPresignedURL(t *testing.T) {
	const signedURL = "https://objects.example.test/upload?part=1&signature=test-only"

	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{
			name:        "nested JSON",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body: fmt.Sprintf(
				`{"error":"invalid_argument","message":"part rejected","details":{"parts":[{"part_number":1,"url":%q,"expires_at":"later"}]}}`,
				signedURL,
			),
		},
		{
			name:        "plain text",
			status:      http.StatusBadGateway,
			contentType: "text/plain",
			body:        "upstream failed for " + signedURL,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			root, tf := rootWithDriveURL(t, srv.URL)
			root.SetArgs([]string{
				"drive", "upload", "parts-presign", "42",
				"--upload-id", "upload-1",
				"--data", `{"part_numbers":[1]}`,
			})

			err := root.Execute()
			if err == nil {
				t.Fatal("expected a backend error")
			}
			ee := output.AsExitError(err)
			if ee == nil {
				t.Fatalf("expected a structured error, got %v", err)
			}
			visible := strings.Join([]string{
				err.Error(),
				ee.Message,
				ee.Hint,
				string(ee.Detail),
				tf.ErrOut.String(),
			}, "\n")
			if strings.Contains(visible, signedURL) {
				t.Fatal("the presigned response URL survived in the structured error or rendered output")
			}
			if !strings.Contains(visible, "REDACTED") {
				t.Fatalf("expected the response URL to be masked, got %q", visible)
			}
		})
	}
}
