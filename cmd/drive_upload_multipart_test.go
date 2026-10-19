package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func writeMultipartTestFile(t *testing.T, payload []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "multipart.bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func multipartPrepareBody(fileID string, partSize, partCount, concurrency uint64) string {
	return fmt.Sprintf(
		`{"file_id":%s,"status":"pending","upload_mode":"multipart","upload_id":"upload-1",`+
			`"part_size":%d,"part_count":%d,"max_concurrent_parts":%d,`+
			`"object_path":"objects/large.bin","content_type":"application/octet-stream"}`,
		fileID, partSize, partCount, concurrency,
	)
}

func decodePartNumbers(t *testing.T, r *http.Request) []uint64 {
	t.Helper()
	var body struct {
		UploadID    string   `json:"upload_id"`
		PartNumbers []uint64 `json:"part_numbers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("decode parts-presign body: %v", err)
	}
	if body.UploadID != "upload-1" {
		t.Errorf("upload_id = %q, want upload-1", body.UploadID)
	}
	if len(body.PartNumbers) == 0 || len(body.PartNumbers) > multipartPresignBatchMax {
		t.Errorf("parts-presign batch size = %d, want 1..%d", len(body.PartNumbers), multipartPresignBatchMax)
	}
	return body.PartNumbers
}

func multipartStoreHandler(
	t *testing.T,
	payload []byte,
	active *int,
	maxActive *int,
	putCounts map[uint64]int,
	mu *sync.Mutex,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		numberText := strings.TrimPrefix(r.URL.Path, "/part/")
		number, err := strconv.ParseUint(numberText, 10, 64)
		if err != nil {
			t.Errorf("unexpected store path %q", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read part %d: %v", number, err)
		}
		wantStart := int((number - 1) * 4)
		wantEnd := wantStart + 4
		if wantEnd > len(payload) {
			wantEnd = len(payload)
		}
		if string(body) != string(payload[wantStart:wantEnd]) {
			t.Errorf("part %d body = %q, want %q", number, body, payload[wantStart:wantEnd])
		}

		mu.Lock()
		*active++
		if *active > *maxActive {
			*maxActive = *active
		}
		putCounts[number]++
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		*active--
		mu.Unlock()
		w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, number))
		w.WriteHeader(http.StatusOK)
	}
}

func TestDriveUploadFile_MultipartHappyPath(t *testing.T) {
	const fileID = "18446744073709551615"
	payload := make([]byte, 41*4)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	src := writeMultipartTestFile(t, payload)

	var mu sync.Mutex
	prepareHeaders := []string{}
	presignBatches := [][]uint64{}
	putCounts := map[uint64]int{}
	active, maxActive := 0, 0
	var completeBody string
	unexpectedCleanup := false
	var env *driveTestEnv

	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/bot/drive/files/prepare-upload":
			mu.Lock()
			prepareHeaders = append(prepareHeaders, r.Header.Get(multipartCapabilityHeader))
			call := len(prepareHeaders)
			mu.Unlock()
			if call == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"client_capability_required","message":"multipart-v1 required","required_capability":"multipart-v1"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(multipartPrepareBody(fileID, 4, 41, 2)))
		case strings.HasSuffix(r.URL.Path, "/upload/parts-presign"):
			numbers := decodePartNumbers(t, r)
			mu.Lock()
			presignBatches = append(presignBatches, append([]uint64(nil), numbers...))
			mu.Unlock()
			var parts []map[string]any
			for _, number := range numbers {
				parts = append(parts, map[string]any{
					"part_number": number,
					"url":         fmt.Sprintf("%s/part/%d", env.store.URL, number),
					"expires_at":  time.Now().Add(time.Hour).Format(time.RFC3339),
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"parts": parts})
		case strings.HasSuffix(r.URL.Path, "/upload/complete"):
			raw, _ := io.ReadAll(r.Body)
			completeBody = string(raw)
			_, _ = w.Write([]byte(`{"file":{"id":` + fileID + `,"parent_id":0,"name":"multipart.bin"},"size":164}`))
		case strings.HasSuffix(r.URL.Path, "/cancel-upload"),
			strings.HasSuffix(r.URL.Path, "/upload/abort"):
			unexpectedCleanup = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}, multipartStoreHandler(t, payload, &active, &maxActive, putCounts, &mu))

	if err := env.run("drive", "upload", "file", src, "--space-id", "shared:s1"); err != nil {
		t.Fatalf("execute multipart upload: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(prepareHeaders) != 2 || prepareHeaders[0] != "" || prepareHeaders[1] != "multipart-v1" {
		t.Fatalf("prepare capability headers = %#v, want [empty multipart-v1]", prepareHeaders)
	}
	if len(presignBatches) != 3 {
		t.Fatalf("presign batches = %v, want 3 batches", presignBatches)
	}
	var flattened []uint64
	for _, batch := range presignBatches {
		if len(batch) > multipartPresignBatchMax {
			t.Errorf("presign batch exceeds 20: %v", batch)
		}
		flattened = append(flattened, batch...)
	}
	if len(flattened) != 41 {
		t.Fatalf("signed part count = %d, want 41", len(flattened))
	}
	for i, number := range flattened {
		if number != uint64(i+1) {
			t.Fatalf("signed part %d = %d, want %d", i, number, i+1)
		}
	}
	if maxActive != 2 {
		t.Errorf("max concurrent part PUTs = %d, want exactly the cap of 2", maxActive)
	}
	if len(putCounts) != 41 {
		t.Errorf("uploaded parts = %d, want 41", len(putCounts))
	}
	for number := uint64(1); number <= 41; number++ {
		if putCounts[number] != 1 {
			t.Errorf("part %d PUT count = %d, want 1", number, putCounts[number])
		}
	}
	if unexpectedCleanup {
		t.Error("a successful multipart upload must never cancel or abort")
	}
	if !strings.Contains(completeBody, `"upload_id":"upload-1"`) {
		t.Errorf("complete body missing upload_id: %s", completeBody)
	}
	for number := 1; number <= 41; number++ {
		want := fmt.Sprintf(`{"etag":"\"etag-%d\"","part_number":%d}`, number, number)
		if !strings.Contains(completeBody, want) {
			t.Fatalf("complete body is not ascending at part %d: %s", number, completeBody)
		}
	}
	if got := env.data(t)["file"].(map[string]any)["id"]; got != fileID {
		t.Errorf("completed file id = %v, want lossless %s", got, fileID)
	}
	if strings.Contains(env.tf.Out.String(), env.store.URL) {
		t.Error("a presigned block URL leaked into stdout")
	}
}

func TestDriveUploadFile_SingleModesDoNotDeclareCapability(t *testing.T) {
	for _, mode := range []string{"single", "", "future-mode"} {
		name := mode
		if name == "" {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			const payload = "single"
			var prepareHeader string
			var storeCalls, confirms int
			var env *driveTestEnv
			env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
					prepareHeader = r.Header.Get(multipartCapabilityHeader)
					modeField := ""
					if mode != "" {
						modeField = `,"upload_mode":"` + mode + `"`
					}
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"file_id":7` + modeField + `,"upload_url":"` + env.store.URL + `/single","content_type":"text/plain"}`))
				case strings.HasSuffix(r.URL.Path, "/confirm-upload"):
					confirms++
					_, _ = w.Write([]byte(`{"id":7,"parent_id":0,"name":"single.bin"}`))
				default:
					t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
				}
			}, func(w http.ResponseWriter, r *http.Request) {
				storeCalls++
				w.WriteHeader(http.StatusOK)
			})

			src := writeMultipartTestFile(t, []byte(payload))
			if err := env.run("drive", "upload", "file", src, "--space-id", "s1"); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if prepareHeader != "" {
				t.Errorf("single/missing/unknown mode sent capability header %q", prepareHeader)
			}
			if storeCalls != 1 || confirms != 1 {
				t.Errorf("single path calls: store=%d confirm=%d, want 1/1", storeCalls, confirms)
			}
		})
	}
}

func TestDriveUploadFile_MultipartPartRetryAndResign(t *testing.T) {
	t.Run("transient failure retries only its part", func(t *testing.T) {
		payload := []byte("abcdefghijkl")
		src := writeMultipartTestFile(t, payload)
		var mu sync.Mutex
		putCounts := map[uint64]int{}
		var presignBatches [][]uint64
		var env *driveTestEnv
		env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
				if r.Header.Get(multipartCapabilityHeader) == "" {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"error":"client_capability_required","message":"multipart-v1 required"}`))
					return
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(multipartPrepareBody("7", 4, 3, 1)))
			case strings.HasSuffix(r.URL.Path, "/upload/parts-presign"):
				numbers := decodePartNumbers(t, r)
				mu.Lock()
				presignBatches = append(presignBatches, append([]uint64(nil), numbers...))
				mu.Unlock()
				var parts []map[string]any
				for _, number := range numbers {
					parts = append(parts, map[string]any{
						"part_number": number,
						"url":         fmt.Sprintf("%s/part/%d", env.store.URL, number),
						"expires_at":  time.Now().Add(time.Hour).Format(time.RFC3339),
					})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"parts": parts})
			case strings.HasSuffix(r.URL.Path, "/upload/complete"):
				_, _ = w.Write([]byte(`{"file":{"id":7,"parent_id":0},"size":12}`))
			default:
				t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
			}
		}, func(w http.ResponseWriter, r *http.Request) {
			number, _ := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/part/"), 10, 64)
			_, _ = io.Copy(io.Discard, r.Body)
			mu.Lock()
			putCounts[number]++
			count := putCounts[number]
			mu.Unlock()
			if number == 2 && count == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, number))
			w.WriteHeader(http.StatusOK)
		})

		if err := env.run("drive", "upload", "file", src, "--space-id", "s1"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if putCounts[1] != 1 || putCounts[2] != 2 || putCounts[3] != 1 {
			t.Errorf("PUT counts = %v, want only part 2 retried", putCounts)
		}
		if len(presignBatches) != 1 {
			t.Errorf("a 500 must retry the same URL, not re-sign: batches=%v", presignBatches)
		}
	})

	t.Run("403 re-signs exactly that part", func(t *testing.T) {
		payload := []byte("abcdefghijkl")
		src := writeMultipartTestFile(t, payload)
		var mu sync.Mutex
		putCounts := map[uint64]int{}
		var presignBatches [][]uint64
		var env *driveTestEnv
		env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
				if r.Header.Get(multipartCapabilityHeader) == "" {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"error":"client_capability_required","message":"multipart-v1 required"}`))
					return
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(multipartPrepareBody("7", 4, 3, 1)))
			case strings.HasSuffix(r.URL.Path, "/upload/parts-presign"):
				numbers := decodePartNumbers(t, r)
				mu.Lock()
				presignBatches = append(presignBatches, append([]uint64(nil), numbers...))
				mu.Unlock()
				var parts []map[string]any
				for _, number := range numbers {
					parts = append(parts, map[string]any{
						"part_number": number,
						"url":         fmt.Sprintf("%s/part/%d", env.store.URL, number),
						"expires_at":  time.Now().Add(time.Hour).Format(time.RFC3339),
					})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"parts": parts})
			case strings.HasSuffix(r.URL.Path, "/upload/complete"):
				_, _ = w.Write([]byte(`{"file":{"id":7,"parent_id":0},"size":12}`))
			default:
				t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
			}
		}, func(w http.ResponseWriter, r *http.Request) {
			number, _ := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/part/"), 10, 64)
			_, _ = io.Copy(io.Discard, r.Body)
			mu.Lock()
			putCounts[number]++
			count := putCounts[number]
			mu.Unlock()
			if number == 2 && count == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, number))
			w.WriteHeader(http.StatusOK)
		})

		if err := env.run("drive", "upload", "file", src, "--space-id", "s1"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(presignBatches) != 2 ||
			fmt.Sprint(presignBatches[0]) != "[1 2 3]" ||
			fmt.Sprint(presignBatches[1]) != "[2]" {
			t.Errorf("presign batches = %v, want initial [1 2 3] then only [2]", presignBatches)
		}
		if putCounts[1] != 1 || putCounts[2] != 2 || putCounts[3] != 1 {
			t.Errorf("PUT counts = %v, want only part 2 retried after re-sign", putCounts)
		}
	})
}

func multipartAPIHandlerForCompletion(
	t *testing.T,
	env **driveTestEnv,
	complete http.HandlerFunc,
	abort *atomic.Int32,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
			if r.Header.Get(multipartCapabilityHeader) == "" {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"client_capability_required","message":"multipart-v1 required"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(multipartPrepareBody("7", 4, 2, 1)))
		case strings.HasSuffix(r.URL.Path, "/upload/parts-presign"):
			numbers := decodePartNumbers(t, r)
			var parts []map[string]any
			for _, number := range numbers {
				parts = append(parts, map[string]any{
					"part_number": number,
					"url":         fmt.Sprintf("%s/part/%d", (*env).store.URL, number),
					"expires_at":  time.Now().Add(time.Hour).Format(time.RFC3339),
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"parts": parts})
		case strings.HasSuffix(r.URL.Path, "/upload/complete"):
			complete(w, r)
		case strings.HasSuffix(r.URL.Path, "/upload/abort"):
			abort.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}
}

func TestDriveUploadFile_CompleteRetrySemantics(t *testing.T) {
	t.Run("explicit HTTP 500 retries to success", func(t *testing.T) {
		src := writeMultipartTestFile(t, []byte("abcdefgh"))
		var completeCalls, aborts atomic.Int32
		var env *driveTestEnv
		env = newDriveTestEnv(t, "bf_bot", multipartAPIHandlerForCompletion(t, &env, func(w http.ResponseWriter, r *http.Request) {
			if completeCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal","message":"temporary failure"}`))
				return
			}
			_, _ = w.Write([]byte(`{"file":{"id":7,"parent_id":0},"size":8}`))
		}, &aborts), func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"etag"`)
			w.WriteHeader(http.StatusOK)
		})

		if err := env.run("drive", "upload", "file", src, "--space-id", "s1"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got := completeCalls.Load(); got != 2 {
			t.Errorf("complete calls = %d, want 2 after one lost response", got)
		}
		if got := aborts.Load(); got != 0 {
			t.Errorf("aborts = %d, want 0 after successful completion", got)
		}
	})

	t.Run("transport failure is one request and RESULT_UNKNOWN", func(t *testing.T) {
		src := writeMultipartTestFile(t, []byte("abcdefgh"))
		var completeCalls, aborts atomic.Int32
		var env *driveTestEnv
		env = newDriveTestEnv(t, "bf_bot", multipartAPIHandlerForCompletion(t, &env, func(w http.ResponseWriter, r *http.Request) {
			completeCalls.Add(1)
			hijacker := w.(http.Hijacker)
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
		}, &aborts), func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"etag"`)
			w.WriteHeader(http.StatusOK)
		})

		err := env.run("drive", "upload", "file", src, "--space-id", "s1")
		if err == nil {
			t.Fatal("expected completion to fail")
		}
		ee := output.AsExitError(err)
		if ee == nil || ee.Code != "RESULT_UNKNOWN" {
			t.Fatalf("code = %v, want RESULT_UNKNOWN", err)
		}
		if got := completeCalls.Load(); got != 1 {
			t.Errorf("complete calls = %d, want 1 because a transport failure must not be retried", got)
		}
		if aborts.Load() != 0 {
			t.Error("an ambiguous completion must not be aborted")
		}
	})
}

func TestDriveUploadFile_UnknownModeAfterCapabilityNegotiationFallsBackToSingle(t *testing.T) {
	const payload = "single"
	var prepareCalls, storeCalls, confirms int
	var capabilityHeaders []string
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
			prepareCalls++
			capabilityHeaders = append(capabilityHeaders, r.Header.Get(multipartCapabilityHeader))
			if prepareCalls == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"client_capability_required","message":"multipart-v1 required"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"file_id":7,"upload_mode":"future-mode","upload_url":"` + env.store.URL + `/single","content_type":"text/plain"}`))
		case strings.HasSuffix(r.URL.Path, "/confirm-upload"):
			confirms++
			_, _ = w.Write([]byte(`{"id":7,"parent_id":0,"name":"single.bin"}`))
		default:
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}, func(w http.ResponseWriter, r *http.Request) {
		storeCalls++
		w.WriteHeader(http.StatusOK)
	})

	src := writeMultipartTestFile(t, []byte(payload))
	if err := env.run("drive", "upload", "file", src, "--space-id", "s1"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if prepareCalls != 2 {
		t.Fatalf("prepare calls = %d, want 2", prepareCalls)
	}
	if len(capabilityHeaders) != 2 || capabilityHeaders[0] != "" || capabilityHeaders[1] != multipartCapabilityValue {
		t.Errorf("prepare capability headers = %#v, want [empty multipart-v1]", capabilityHeaders)
	}
	if storeCalls != 1 || confirms != 1 {
		t.Errorf("single fallback calls: store=%d confirm=%d, want 1/1", storeCalls, confirms)
	}
}

func TestDriveUploadFile_DirectMultipartResponsePinsCurrentConflict(t *testing.T) {
	src := writeMultipartTestFile(t, []byte("abcdefgh"))
	prepareCalls, abortCalls, presignCalls := 0, 0, 0
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
			prepareCalls++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(multipartPrepareBody("7", 4, 2, 1)))
		case strings.HasSuffix(r.URL.Path, "/upload/abort"):
			abortCalls++
			_, _ = w.Write([]byte(`{"ok":true}`))
		case strings.HasSuffix(r.URL.Path, "/upload/parts-presign"):
			presignCalls++
			t.Error("direct multipart response must not presign until the capability contract is decided")
		default:
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}, nil)

	err := env.run("drive", "upload", "file", src, "--space-id", "s1")
	if err == nil {
		t.Fatal("expected UPLOAD_MODE_CONFLICT for a direct multipart response")
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "UPLOAD_MODE_CONFLICT" {
		t.Fatalf("code = %v, want UPLOAD_MODE_CONFLICT", err)
	}
	if prepareCalls != 1 || abortCalls != 1 || presignCalls != 0 {
		t.Errorf("calls: prepare=%d abort=%d presign=%d, want 1/1/0", prepareCalls, abortCalls, presignCalls)
	}
}

func TestDriveUploadFile_CompleteErrorDoesNotLeakSignedURL(t *testing.T) {
	const marker = "COMPLETE_SIGNATURE_MUST_NOT_LEAK"
	signedURL := "https://storage.example/upload?sig=" + marker
	src := writeMultipartTestFile(t, []byte("abcdefgh"))
	var completeCalls, aborts atomic.Int32
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", multipartAPIHandlerForCompletion(t, &env, func(w http.ResponseWriter, r *http.Request) {
		completeCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, `{"error":"internal","message":"completion failed","details":{"url":%q}}`, signedURL)
	}, &aborts), func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(http.StatusOK)
	})

	err := env.run("drive", "upload", "file", src, "--space-id", "s1")
	if err == nil {
		t.Fatal("expected completion to fail")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected structured error, got %v", err)
	}
	var structured map[string]any
	if decodeErr := json.Unmarshal(env.tf.ErrOut.Bytes(), &structured); decodeErr != nil {
		t.Fatalf("parse structured stderr: %v (%s)", decodeErr, env.tf.ErrOut.String())
	}
	visible := err.Error() + "\n" + ee.Message + "\n" + ee.Hint + "\n" + string(ee.Detail) + "\n" +
		env.tf.Out.String() + "\n" + env.tf.ErrOut.String()
	hits := strings.Count(visible, marker)
	t.Logf("complete signed URL marker hits = %d", hits)
	if hits != 0 {
		t.Errorf("complete signed URL marker hits = %d, want 0: %s", hits, visible)
	}
	if got := completeCalls.Load(); got != multipartCompleteAttempts {
		t.Errorf("complete calls = %d, want finite retry total %d", got, multipartCompleteAttempts)
	}
	if aborts.Load() != 1 {
		t.Errorf("aborts = %d, want 1 after explicit 5xx retries are exhausted", aborts.Load())
	}
}

func TestDriveUploadFile_InterruptedMultipartAbortsWithDetachedContext(t *testing.T) {
	src := writeMultipartTestFile(t, []byte("abcdefgh"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	aborted := false
	abortContextLive := false
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
			if r.Header.Get(multipartCapabilityHeader) == "" {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"client_capability_required","message":"multipart-v1 required"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(multipartPrepareBody("7", 4, 2, 1)))
		case strings.HasSuffix(r.URL.Path, "/upload/parts-presign"):
			numbers := decodePartNumbers(t, r)
			var parts []map[string]any
			for _, number := range numbers {
				parts = append(parts, map[string]any{
					"part_number": number,
					"url":         fmt.Sprintf("%s/part/%d", env.store.URL, number),
					"expires_at":  time.Now().Add(time.Hour).Format(time.RFC3339),
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"parts": parts})
		case strings.HasSuffix(r.URL.Path, "/upload/abort"):
			aborted = true
			if r.Context().Err() == nil {
				abortContextLive = true
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	})

	env.root.SetArgs([]string{"drive", "upload", "file", src, "--space-id", "s1"})
	if err := env.root.ExecuteContext(ctx); err == nil {
		t.Fatal("the interrupted multipart upload must fail")
	}
	if !aborted {
		t.Error("the incomplete multipart upload was not aborted")
	}
	if !abortContextLive {
		t.Error("abort used the cancelled command context instead of a detached cleanup context")
	}
}

func TestDriveUploadFile_PrepareTooLargeReportsLimitWithoutPUT(t *testing.T) {
	src := writeMultipartTestFile(t, []byte("abcdefghijkl"))
	var env *driveTestEnv
	prepareCalls, cleanupCalls := 0, 0
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/prepare-upload") {
			prepareCalls++
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error":"file_too_large","message":"file too large","max_size_kb":1024,"size":12,"required_capability":"multipart-v1"}`))
			return
		}
		if strings.Contains(r.URL.Path, "cancel-upload") || strings.HasSuffix(r.URL.Path, "/upload/abort") {
			cleanupCalls++
		}
	}, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no object PUT may run after a 413 prepare refusal")
	})

	err := env.run("drive", "upload", "file", src, "--space-id", "s1")
	if err == nil {
		t.Fatal("expected file_too_large")
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "file_too_large" {
		t.Fatalf("code = %v, want file_too_large", err)
	}
	visible := err.Error() + "\n" + ee.Message + "\n" + ee.Hint + "\n" + string(ee.Detail) + "\n" + env.tf.ErrOut.String()
	if !strings.Contains(visible, "1024 KiB") || !strings.Contains(visible, "declared size 12 bytes") {
		t.Errorf("413 error omitted the effective limit or declared size: %s", visible)
	}
	if prepareCalls != 1 {
		t.Errorf("prepare calls = %d, want 1; 413 is not a capability handshake", prepareCalls)
	}
	if cleanupCalls != 0 {
		t.Errorf("cleanup calls = %d, want 0 because no pending row exists", cleanupCalls)
	}
}

func TestDriveUploadFile_MultipartErrorDoesNotLeakPartURL(t *testing.T) {
	const marker = "PART_SIGNATURE_MUST_NOT_LEAK"
	src := writeMultipartTestFile(t, []byte("abcdefgh"))
	aborts := 0
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
			if r.Header.Get(multipartCapabilityHeader) == "" {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"client_capability_required","message":"multipart-v1 required"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(multipartPrepareBody("7", 4, 2, 1)))
		case strings.HasSuffix(r.URL.Path, "/upload/parts-presign"):
			numbers := decodePartNumbers(t, r)
			var parts []map[string]any
			for _, number := range numbers {
				parts = append(parts, map[string]any{
					"part_number": number,
					"url":         fmt.Sprintf("%s/part/%d?sig=%s", env.store.URL, number, marker),
					"expires_at":  time.Now().Add(time.Hour).Format(time.RFC3339),
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"parts": parts})
		case strings.HasSuffix(r.URL.Path, "/upload/abort"):
			aborts++
			_, _ = w.Write([]byte(`{"ok":true}`))
		case strings.HasSuffix(r.URL.Path, "/upload/complete"):
			t.Error("completion must not run when a part is terminally rejected")
		default:
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
	})

	err := env.run("drive", "upload", "file", src, "--space-id", "s1")
	if err == nil {
		t.Fatal("expected the part PUT to fail")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected structured error, got %v", err)
	}
	visible := err.Error() + "\n" + ee.Message + "\n" + ee.Hint + "\n" + string(ee.Detail) + "\n" +
		env.tf.Out.String() + "\n" + env.tf.ErrOut.String()
	if strings.Contains(visible, marker) {
		t.Errorf("presigned part URL leaked into visible output: %s", visible)
	}
	if aborts != 1 {
		t.Errorf("aborts = %d, want 1 for the incomplete session", aborts)
	}
}
