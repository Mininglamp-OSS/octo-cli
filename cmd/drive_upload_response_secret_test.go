package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func configureDriveResponseSecretTestClient(env *driveTestEnv, verbose bool) {
	const token = "bf_bot"
	cfg := &config.Config{APIBaseURL: env.api.URL, BotToken: token, Format: "json"}
	cred := &credential.BotCredential{Token: token, Source: "test"}
	env.tf.SetClient(client.New(cfg, cred, client.Options{
		NoRetry: true,
		Verbose: verbose,
		ErrOut:  env.tf.ErrOut,
	}))
}

type responseSecretLeakCase struct {
	name        string
	status      int
	contentType string
	body        string
	location    string
}

func responseSecretLeakCases(secretField, marker string) []responseSecretLeakCase {
	signedURL := "https://objects.example.test/upload?" + marker
	redirectURL := "https://objects.example.test/redirect?" + marker
	return []responseSecretLeakCase{
		{
			name:        "JSON body",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body: fmt.Sprintf(
				`{"error":"invalid_argument","message":"upload rejected","details":{%q:%q}}`,
				secretField, signedURL,
			),
		},
		{
			name:        "plain text body",
			status:      http.StatusBadGateway,
			contentType: "text/plain",
			body:        "storage failed for " + signedURL,
		},
		{
			name:     "valid redirect",
			status:   http.StatusFound,
			location: redirectURL,
			body:     redirectURL,
		},
		{
			name:     "malformed redirect",
			status:   http.StatusFound,
			location: redirectURL + "=%zz",
		},
	}
}

func assertNoResponseSecretVisible(t *testing.T, env *driveTestEnv, err error, marker string) {
	t.Helper()
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected a structured error, got %v", err)
	}
	visible := strings.Join([]string{
		err.Error(),
		ee.Message,
		ee.Hint,
		string(ee.Detail),
		env.tf.Out.String(),
		env.tf.ErrOut.String(),
	}, "\n")
	if strings.Contains(visible, marker) {
		t.Fatalf("response secret reached visible output:\n%s", visible)
	}
}

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

func TestDriveUploadFile_PrepareResponseSecretNeverLeaksToDefaultOrVerboseStderr(t *testing.T) {
	const marker = "composite-response-secret-53223604"

	src := filepath.Join(t.TempDir(), "large.bin")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range responseSecretLeakCases("upload_url", marker) {
		for _, verbose := range []bool{false, true} {
			name := fmt.Sprintf("%s/verbose=%t", tc.name, verbose)
			t.Run(name, func(t *testing.T) {
				env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
					if tc.contentType != "" {
						w.Header().Set("Content-Type", tc.contentType)
					}
					if tc.location != "" {
						w.Header().Set("Location", tc.location)
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}, nil)
				configureDriveResponseSecretTestClient(env, verbose)

				args := []string{"drive", "upload", "file", src, "--space-id", "shared:s1"}
				if verbose {
					args = append([]string{"--verbose"}, args...)
				}
				err := env.run(args...)
				if err == nil {
					t.Fatal("expected the composite prepare request to fail")
				}
				assertNoResponseSecretVisible(t, env, err, marker)
			})
		}
	}
}

func TestAPI_PartsPresignResponseSecretNeverLeaksToDefaultOrVerboseStderr(t *testing.T) {
	const marker = "generic-api-response-secret-53223604"

	for _, tc := range responseSecretLeakCases("url", marker) {
		for _, verbose := range []bool{false, true} {
			name := fmt.Sprintf("%s/verbose=%t", tc.name, verbose)
			t.Run(name, func(t *testing.T) {
				env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
					if tc.contentType != "" {
						w.Header().Set("Content-Type", tc.contentType)
					}
					if tc.location != "" {
						w.Header().Set("Location", tc.location)
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}, nil)
				configureDriveResponseSecretTestClient(env, verbose)

				args := []string{
					"api", http.MethodPost, "/v1/user/drive/files/42/upload/parts-presign",
					"--data", `{"upload_id":"upload-1","part_numbers":[1]}`,
				}
				if verbose {
					args = append([]string{"--verbose"}, args...)
				}
				err := env.run(args...)
				if err == nil {
					t.Fatal("expected the generic API request to fail")
				}
				assertNoResponseSecretVisible(t, env, err, marker)
			})
		}
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
