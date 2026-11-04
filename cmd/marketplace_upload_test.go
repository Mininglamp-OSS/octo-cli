package cmd

import (
	"encoding/json"
	"fmt"
	"io"
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

func marketplaceArchive(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skill.zip")
	if err := os.WriteFile(path, []byte("skill archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func skillTarget(w http.ResponseWriter, url string, headers map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": marketplaceUploadTarget{
		UploadID: "upload-1", URL: url, Method: "PUT", Headers: headers,
	}})
}

func TestMarketplaceSkillUploadFile(t *testing.T) {
	path := marketplaceArchive(t)
	var targetURL string
	calls := 0
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/market/api/v1/skill_uploads" {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer bf_bot" || r.Header.Get("X-Space-Id") != "test-space" {
			t.Errorf("missing API identity: %v", r.Header)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["file_name"] != "skill.zip" || body["file_size"] != float64(len("skill archive bytes")) {
			t.Errorf("unexpected initialization body: %v", body)
		}
		skillTarget(w, targetURL, map[string]string{"Content-Type": "application/zip", "X-Amz-Meta-Test": "signed-value"})
	}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "PUT" || string(body) != "skill archive bytes" || r.ContentLength != int64(len(body)) {
			t.Errorf("incorrect object transfer: %s length=%d body=%s", r.Method, r.ContentLength, body)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Space-Id") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("Octo credentials leaked to storage: %v", r.Header)
		}
		if r.Header.Get("Content-Type") != "application/zip" || r.Header.Get("X-Amz-Meta-Test") != "signed-value" {
			t.Errorf("signed headers not echoed: %v", r.Header)
		}
		if r.URL.Query().Get("X-Amz-Credential") != "full-test-credential" {
			t.Errorf("URL was redacted before PUT: %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
	})
	targetURL = env.store.URL + "/object?X-Amz-Credential=full-test-credential&X-Amz-Signature=full-test-signature"
	cfg := &config.Config{APIBaseURL: env.api.URL, BotToken: "bf_bot", Format: "json"}
	cred := &credential.BotCredential{Token: "bf_bot", SpaceID: "test-space", Source: "test"}
	env.tf.SetCredential(cred)
	env.tf.SetClient(client.New(cfg, cred, client.Options{Verbose: true, ErrOut: env.tf.ErrOut}))
	if err := env.run("--verbose", "marketplace", "skill-upload", "create", "--file", path); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(env.storeRequests) != 1 {
		t.Fatalf("expected initialization + PUT only: api=%d store=%d", calls, len(env.storeRequests))
	}
	data := env.data(t)
	if data["skill_upload_id"] != "upload-1" || data["uploaded_bytes"] != float64(len("skill archive bytes")) {
		t.Errorf("unexpected result: %v", data)
	}
	visible := env.tf.Out.String() + env.tf.ErrOut.String()
	for _, secret := range []string{targetURL, "full-test-credential", "full-test-signature", "signed-value"} {
		if strings.Contains(visible, secret) {
			t.Errorf("output exposed transfer secret %q", secret)
		}
	}
}

func TestMarketplaceSkillUploadDryRunAndInvalidFiles(t *testing.T) {
	valid := marketplaceArchive(t)
	empty := filepath.Join(t.TempDir(), "empty.zip")
	_ = os.WriteFile(empty, nil, 0o600)
	for _, tc := range []struct {
		name, path   string
		dry, wantErr bool
	}{
		{"dry run", valid, true, false},
		{"missing", valid + "-missing.zip", false, true},
		{"directory", t.TempDir(), false, true},
		{"empty", empty, false, true},
		{"extension", "bad.txt", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			env := newDriveTestEnv(t, "bf_bot", func(http.ResponseWriter, *http.Request) { calls++ }, nil)
			args := []string{"marketplace", "skill-upload", "create", "--file", tc.path}
			if tc.dry {
				args = append([]string{"--dry-run"}, args...)
			}
			err := env.run(args...)
			if (err != nil) != tc.wantErr || calls != 0 || len(env.storeRequests) != 0 {
				t.Fatalf("error=%v API calls=%d storage=%d", err, calls, len(env.storeRequests))
			}
		})
	}
}

func TestMarketplaceSkillUploadFailureDoesNotParse(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusAccepted, http.StatusNoContent, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var target string
			calls := 0
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				skillTarget(w, target, nil)
			}, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "https://example.test/?signature=storage-secret")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("storage-secret"))
			})
			target = env.store.URL + "/object?signature=storage-secret"
			err := env.run("marketplace", "skill-upload", "create", "--file", marketplaceArchive(t))
			if err == nil || calls != 1 || len(env.storeRequests) != 1 || env.tf.Out.Len() != 0 {
				t.Fatalf("err=%v API=%d storage=%d stdout=%s", err, calls, len(env.storeRequests), env.tf.Out.String())
			}
			ee := output.AsExitError(err)
			if ee == nil || !strings.Contains(string(ee.Detail), "upload-1") || strings.Contains(ee.Hint, "cancelled") {
				t.Fatalf("missing honest pending-upload outcome: %v", err)
			}
			if strings.Contains(err.Error()+env.tf.ErrOut.String(), "storage-secret") {
				t.Fatal("storage failure leaked signed URL or response body")
			}
		})
	}
}

func TestMarketplaceSkillUploadInitializationErrorsAreRedacted(t *testing.T) {
	for _, tc := range responseSecretLeakCases("presigned_url", "marketplace-secret-marker") {
		t.Run(tc.name, func(t *testing.T) {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("Location", tc.location)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}, nil)
			configureDriveResponseSecretTestClient(env, true)
			err := env.run("--verbose", "marketplace", "skill-upload", "create", "--file", marketplaceArchive(t))
			assertNoResponseSecretVisible(t, env, err, "marketplace-secret-marker")
			if len(env.storeRequests) != 0 {
				t.Fatal("storage called after initialization failed")
			}
		})
	}
}

func TestMarketplaceSkillUploadRefusesInvalidTargetsAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, url, method string
		headers           map[string]string
	}{
		{"URL", "file:///etc/passwd", "PUT", nil},
		{"method", "https://example.test/object", "POST", nil},
		{"authorization", "https://example.test/object", "PUT", map[string]string{"authorization": "must-not-forward"}},
		{"space", "https://example.test/object", "PUT", map[string]string{"X-Space-Id": "must-not-forward"}},
		{"length", "https://example.test/object", "PUT", map[string]string{"Content-Length": "999"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": marketplaceUploadTarget{
					UploadID: "upload-1", URL: tc.url, Method: tc.method, Headers: tc.headers,
				}})
			}, nil)
			if err := env.run("marketplace", "skill-upload", "create", "--file", marketplaceArchive(t)); err == nil {
				t.Fatal("invalid transfer accepted")
			}
			if len(env.storeRequests) != 0 {
				t.Fatal("invalid transfer attempted storage")
			}
		})
	}
}

func TestMarketplaceSkillUploadStandalonePrefix(t *testing.T) {
	t.Setenv("OCTO_MARKETPLACE_API_PREFIX", "")
	var target string
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/skill_uploads" {
			t.Errorf("standalone prefix ignored: %s", r.URL.Path)
		}
		skillTarget(w, target, nil)
	}, nil)
	target = env.store.URL + "/object"
	if err := env.run("marketplace", "skill-upload", "create", "--file", marketplaceArchive(t)); err != nil {
		t.Fatal(err)
	}
}

func TestMarketplaceSkillUploadInitializationIsNotRetried(t *testing.T) {
	calls := 0
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"UPSTREAM_UNAVAILABLE","message":"temporary failure"}}`))
	}, nil)
	if err := env.run("marketplace", "skill-upload", "create", "--file", marketplaceArchive(t)); err == nil {
		t.Fatal("initialization failure reported success")
	}
	if calls != 1 || len(env.storeRequests) != 0 {
		t.Fatalf("unsafe initialization retry: API=%d store=%d", calls, len(env.storeRequests))
	}
}

func TestMarketplaceSkillUploadMetadataOnlyStillWorks(t *testing.T) {
	calls := 0
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["file_name"] != "manual.zip" || body["file_size"] != float64(123) {
			t.Errorf("wrong metadata: %v", body)
		}
		skillTarget(w, "https://example.test/upload?X-Amz-Signature=secret", nil)
	}, nil)
	if err := env.run("marketplace", "skill-upload", "create", "--file-name", "manual.zip", "--file-size", "123"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(env.storeRequests) != 0 {
		t.Fatalf("metadata-only unexpectedly uploaded: API=%d storage=%d", calls, len(env.storeRequests))
	}
	data, ok := env.data(t)["data"].(map[string]any)
	if !ok || data["skill_upload_id"] != "upload-1" {
		t.Fatal("metadata-only response envelope changed")
	}
}

func TestMarketplaceSkillUploadRejectsAmbiguousMetadata(t *testing.T) {
	for _, flag := range []string{"--file-name", "--file-size", "--data"} {
		t.Run(flag, func(t *testing.T) {
			calls := 0
			env := newDriveTestEnv(t, "bf_bot", func(http.ResponseWriter, *http.Request) { calls++ }, nil)
			value := "other.zip"
			if flag == "--file-size" {
				value = "1"
			}
			if flag == "--data" {
				value = `{ "file_name":"other.zip", "file_size":1 }`
			}
			err := env.run("marketplace", "skill-upload", "create", "--file", marketplaceArchive(t), flag, value)
			if err == nil || calls != 0 || len(env.storeRequests) != 0 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}
