package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	tf.SetClient(client.New(cfg, cred, client.Options{NoRetry: true, ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	return root, tf
}

func rootWithDriveURLForResponseSecretTest(
	t *testing.T,
	baseURL string,
	verbose bool,
) (*cobra.Command, *cmdutil.TestFactory) {
	t.Helper()
	root, tf := rootWithDriveURL(t, baseURL)
	const token = "bf_bot"
	cfg := &config.Config{APIBaseURL: baseURL, BotToken: token, Format: "json"}
	cred := &credential.BotCredential{Token: token, Source: "test"}
	tf.SetClient(client.New(cfg, cred, client.Options{
		NoRetry: true,
		Verbose: verbose,
		ErrOut:  tf.ErrOut,
	}))
	return root, tf
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

func TestDrive_PrepareUploadURLIsDeclaredResponseSecret(t *testing.T) {
	op, ok := registry.MustNew().GetOperation("drive.upload.prepare")
	if !ok || op.ResponseSchema == nil {
		t.Fatal("drive.upload.prepare response schema not found")
	}
	uploadURL, ok := op.ResponseSchema.Properties["upload_url"]
	if !ok || !uploadURL.Secret {
		t.Fatal("drive.upload.prepare upload_url must be x-octo-secret")
	}
	if got, want := op.ResponseSecretFields, []string{"upload_url"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("response secret fields = %v, want %v", got, want)
	}
}

func visibleDriveError(err error, tf *cmdutil.TestFactory) string {
	ee := output.AsExitError(err)
	if ee == nil {
		return err.Error() + "\n" + tf.ErrOut.String()
	}
	return strings.Join([]string{
		err.Error(),
		ee.Message,
		ee.Hint,
		string(ee.Detail),
		tf.ErrOut.String(),
	}, "\n")
}

func TestDrive_PrepareErrorRedactsPresignedURL(t *testing.T) {
	const signedURL = "https://objects.example.test/prepare?signature=test-only"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w,
			`{"error":"invalid_argument","message":"prepare failed for %s","details":{"upload_url":%q}}`,
			signedURL, signedURL)
	}))
	t.Cleanup(srv.Close)

	root, tf := rootWithDriveURL(t, srv.URL)
	root.SetArgs([]string{
		"drive", "upload", "prepare",
		"--space-id", "shared:s1",
		"--name", "large.bin",
		"--size", "134217728",
		"--content-type", "application/octet-stream",
	})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected a backend error")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected a structured error, got %v", err)
	}
	if strings.Contains(string(ee.Detail), signedURL) {
		t.Fatal("prepare upload_url survived in Detail")
	}
	if rendered := tf.ErrOut.String(); strings.Contains(rendered, signedURL) {
		t.Fatal("prepare upload_url survived in rendered stderr")
	}
	if visible := visibleDriveError(err, tf); strings.Contains(visible, signedURL) {
		t.Fatal("prepare upload_url survived in the visible error")
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
			status:      http.StatusBadRequest,
			contentType: "text/plain",
			body:        "upstream failed for " + signedURL,
		},
		{
			name:        "invalid JSON",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body:        `{"error":"part rejected","message":"` + signedURL,
		},
		{
			name:        "JSON object key",
			status:      http.StatusInternalServerError,
			contentType: "application/json",
			body:        `{"error":"internal","message":"part batch failed","details":{"` + signedURL + `":{"part_number":1}}}`,
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
			if strings.Contains(string(ee.Detail), signedURL) {
				t.Fatal("the presigned response URL survived in Detail")
			}
			if rendered := tf.ErrOut.String(); strings.Contains(rendered, signedURL) {
				t.Fatal("the presigned response URL survived in rendered stderr")
			}
			visible := visibleDriveError(err, tf)
			if strings.Contains(visible, signedURL) {
				t.Fatal("the presigned response URL survived in the structured error or rendered output")
			}
			if !strings.Contains(visible, "REDACTED") {
				t.Fatalf("expected the response URL to be masked, got %q", visible)
			}
		})
	}
}

func TestDrive_PartsPresignRedirectRedactsLocation(t *testing.T) {
	const signedURL = "https://objects.example.test/redirect?signature=test-only"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", signedURL)
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte(signedURL))
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
		t.Fatal("expected a redirect error")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected a structured error, got %v", err)
	}
	if strings.Contains(string(ee.Detail), signedURL) || strings.Contains(tf.ErrOut.String(), signedURL) {
		t.Fatal("redirect Location survived in Detail or rendered stderr")
	}
	visible := visibleDriveError(err, tf)
	if strings.Contains(visible, signedURL) {
		t.Fatal("redirect Location survived in the visible error")
	}
	if !strings.Contains(visible, "REDACTED") {
		t.Fatalf("expected redirect Location to fail closed, got %q", visible)
	}
}

func TestDrive_PartsPresignResponseSecretNeverLeaksToDefaultOrVerboseStderr(t *testing.T) {
	const marker = "service-response-secret-53223604"

	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		location    string
	}{
		{
			name:        "JSON body",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body: `{"error":"invalid_argument","message":"part rejected","details":{"parts":[` +
				`{"part_number":1,"url":"https://objects.example.test/upload?` + marker + `"}]}}`,
		},
		{
			name:        "plain text body",
			status:      http.StatusBadGateway,
			contentType: "text/plain",
			body:        "storage failed for https://objects.example.test/upload?" + marker,
		},
		{
			name:     "valid redirect",
			status:   http.StatusFound,
			location: "https://objects.example.test/redirect?" + marker,
			body:     "https://objects.example.test/redirect?" + marker,
		},
		{
			name:     "malformed redirect",
			status:   http.StatusFound,
			location: "https://objects.example.test/redirect?" + marker + "=%zz",
		},
	}

	for _, tc := range tests {
		for _, verbose := range []bool{false, true} {
			name := fmt.Sprintf("%s/verbose=%t", tc.name, verbose)
			t.Run(name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tc.contentType != "" {
						w.Header().Set("Content-Type", tc.contentType)
					}
					if tc.location != "" {
						w.Header().Set("Location", tc.location)
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}))
				t.Cleanup(srv.Close)

				root, tf := rootWithDriveURLForResponseSecretTest(t, srv.URL, verbose)
				tf.Globals.Verbose = verbose
				args := []string{
					"drive", "upload", "parts-presign", "42",
					"--upload-id", "upload-1",
					"--data", `{"part_numbers":[1]}`,
				}
				root.SetArgs(args)

				err := root.Execute()
				if err == nil {
					t.Fatal("expected the response to fail")
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
					tf.Out.String(),
					tf.ErrOut.String(),
				}, "\n")
				if strings.Contains(visible, marker) {
					t.Fatalf("response secret reached visible output:\n%s", visible)
				}
			})
		}
	}
}
