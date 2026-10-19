package cmd

import (
	"bytes"
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
		if !bytes.Equal(body, payload[wantStart:wantEnd]) {
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
			mu.Unlock()
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
	t.Logf("multipart prepare capability headers = %#v", prepareHeaders)
	if len(prepareHeaders) != 1 || prepareHeaders[0] != multipartCapabilityValue {
		t.Fatalf("prepare capability headers = %#v, want [multipart-v1]", prepareHeaders)
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

func TestDriveUploadFile_SingleModesDeclareCapabilityOnce(t *testing.T) {
	for _, mode := range []string{"single", "", "future-mode"} {
		name := mode
		if name == "" {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			const payload = "single"
			var prepareHeader string
			var prepareCalls, storeCalls, confirms int
			var env *driveTestEnv
			env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
					prepareCalls++
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
			t.Logf("mode=%q prepare header=%q prepare calls=%d store=%d confirm=%d", mode, prepareHeader, prepareCalls, storeCalls, confirms)
			if prepareHeader != multipartCapabilityValue {
				t.Errorf("single/missing/unknown prepare capability header = %q, want %q", prepareHeader, multipartCapabilityValue)
			}
			if prepareCalls != 1 {
				t.Errorf("prepare calls = %d, want 1", prepareCalls)
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
				if got := r.Header.Get(multipartCapabilityHeader); got != multipartCapabilityValue {
					t.Errorf("prepare capability header = %q, want %q", got, multipartCapabilityValue)
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
				if got := r.Header.Get(multipartCapabilityHeader); got != multipartCapabilityValue {
					t.Errorf("prepare capability header = %q, want %q", got, multipartCapabilityValue)
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
			if got := r.Header.Get(multipartCapabilityHeader); got != multipartCapabilityValue {
				t.Errorf("prepare capability header = %q, want %q", got, multipartCapabilityValue)
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
		if got := completeCalls.Load(); got != 1 {
			t.Fatalf("complete calls = %d, want 1 because a transport failure must not be retried", got)
		}
		ee := output.AsExitError(err)
		if ee == nil || ee.Code != "RESULT_UNKNOWN" {
			t.Fatalf("code = %v, want RESULT_UNKNOWN", err)
		}
		if aborts.Load() != 0 {
			t.Error("an ambiguous completion must not be aborted")
		}
	})
}

func TestDriveUploadFile_UnknownModeFallsBackToSingle(t *testing.T) {
	const payload = "single"
	var prepareCalls, storeCalls, confirms int
	var capabilityHeaders []string
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
			prepareCalls++
			capabilityHeaders = append(capabilityHeaders, r.Header.Get(multipartCapabilityHeader))
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
	if prepareCalls != 1 {
		t.Fatalf("prepare calls = %d, want 1", prepareCalls)
	}
	if len(capabilityHeaders) != 1 || capabilityHeaders[0] != multipartCapabilityValue {
		t.Errorf("prepare capability headers = %#v, want [multipart-v1]", capabilityHeaders)
	}
	if storeCalls != 1 || confirms != 1 {
		t.Errorf("single fallback calls: store=%d confirm=%d, want 1/1", storeCalls, confirms)
	}
}

func TestDriveUploadFile_UnknownModeWithoutUploadURLIsModeConflict(t *testing.T) {
	var prepareCalls, cancelCalls int
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
			prepareCalls++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"file_id":7,"upload_mode":"future-mode","content_type":"text/plain"}`))
		case strings.HasSuffix(r.URL.Path, "/cancel-upload"):
			cancelCalls++
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no object PUT may run without a usable single upload_url")
	})

	src := writeMultipartTestFile(t, []byte("single"))
	err := env.run("drive", "upload", "file", src, "--space-id", "s1")
	if err == nil {
		t.Fatal("expected upload mode conflict")
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "UPLOAD_MODE_CONFLICT" {
		t.Fatalf("code = %v, want UPLOAD_MODE_CONFLICT", err)
	}
	if !strings.Contains(ee.Message, "neither single nor multipart") ||
		!strings.Contains(ee.Message, "did not include upload_url") {
		t.Errorf("message = %q, want the unknown-mode/no-upload_url explanation", ee.Message)
	}
	if prepareCalls != 1 {
		t.Errorf("prepare calls = %d, want 1", prepareCalls)
	}
	if cancelCalls != 1 {
		t.Errorf("cancel calls = %d, want 1 for the pending row", cancelCalls)
	}
}

func TestValidatePresignedPartsRejectsInvalidExpiry(t *testing.T) {
	_, err := validatePresignedParts([]struct {
		PartNumber uint64 `json:"part_number"`
		URL        string `json:"url"`
		ExpiresAt  string `json:"expires_at"`
	}{
		{PartNumber: 1, URL: "https://storage.example/part-1", ExpiresAt: "not-a-timestamp"},
	}, []uint64{1})
	if err == nil {
		t.Fatal("expected invalid expires_at to fail validation")
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "RESPONSE_DECODE" {
		t.Fatalf("code = %v, want RESPONSE_DECODE", err)
	}
	if !strings.Contains(ee.Message, "invalid expires_at for part 1") {
		t.Errorf("message = %q, want invalid expires_at detail", ee.Message)
	}
}

func TestDriveUploadFile_DirectMultipartResponseUsesMultipartPath(t *testing.T) {
	payload := []byte("abcdefgh")
	src := writeMultipartTestFile(t, payload)
	var mu sync.Mutex
	prepareCalls, abortCalls, presignCalls, completeCalls := 0, 0, 0, 0
	putCounts := map[uint64]int{}
	active, maxActive := 0, 0
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
			mu.Lock()
			prepareCalls++
			mu.Unlock()
			if got := r.Header.Get(multipartCapabilityHeader); got != multipartCapabilityValue {
				t.Errorf("prepare capability header = %q, want %q", got, multipartCapabilityValue)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(multipartPrepareBody("7", 4, 2, 1)))
		case strings.HasSuffix(r.URL.Path, "/upload/abort"):
			mu.Lock()
			abortCalls++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true}`))
		case strings.HasSuffix(r.URL.Path, "/upload/parts-presign"):
			mu.Lock()
			presignCalls++
			mu.Unlock()
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
		case strings.HasSuffix(r.URL.Path, "/upload/complete"):
			mu.Lock()
			completeCalls++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"file":{"id":7,"parent_id":0},"size":8}`))
		default:
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}, multipartStoreHandler(t, payload, &active, &maxActive, putCounts, &mu))

	if err := env.run("drive", "upload", "file", src, "--space-id", "s1"); err != nil {
		t.Fatalf("execute direct multipart upload: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("direct multipart calls: prepare=%d presign=%d complete=%d abort=%d", prepareCalls, presignCalls, completeCalls, abortCalls)
	if prepareCalls != 1 || presignCalls != 1 || completeCalls != 1 || abortCalls != 0 {
		t.Errorf("calls: prepare=%d presign=%d complete=%d abort=%d, want 1/1/1/0",
			prepareCalls, presignCalls, completeCalls, abortCalls)
	}
	if len(putCounts) != 2 || putCounts[1] != 1 || putCounts[2] != 1 {
		t.Errorf("PUT counts = %v, want exactly one PUT each for parts 1 and 2", putCounts)
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

func TestDriveUploadFile_AbortErrorDoesNotLeakSignedURL(t *testing.T) {
	const marker = "ABORT_SIGNATURE_MUST_NOT_LEAK"
	signedURL := "https://storage.example/upload?sig=" + marker
	cases := []struct {
		name        string
		status      int
		contentType string
		body        string
		location    string
	}{
		{
			name:        "error body",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body: fmt.Sprintf(
				`{"error":"invalid_argument","message":"abort rejected %s","details":{"url":%q}}`,
				signedURL, signedURL,
			),
		},
		{
			name:     "valid redirect",
			status:   http.StatusFound,
			location: signedURL,
		},
		{
			name:     "malformed redirect",
			status:   http.StatusFound,
			location: signedURL + "#%zz",
		},
	}

	for _, tc := range cases {
		for _, verbose := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/verbose=%t", tc.name, verbose), func(t *testing.T) {
				src := writeMultipartTestFile(t, []byte("abcdefgh"))
				var abortCalls atomic.Int32
				var env *driveTestEnv
				env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
					switch {
					case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
						w.WriteHeader(http.StatusCreated)
						_, _ = w.Write([]byte(multipartPrepareBody("7", 8, 1, 1)))
					case strings.HasSuffix(r.URL.Path, "/upload/parts-presign"):
						_ = json.NewEncoder(w).Encode(map[string]any{
							"parts": []map[string]any{{
								"part_number": 1,
								"url":         env.store.URL + "/part/1",
								"expires_at":  time.Now().Add(time.Hour).Format(time.RFC3339),
							}},
						})
					case strings.HasSuffix(r.URL.Path, "/upload/abort"):
						abortCalls.Add(1)
						if tc.contentType != "" {
							w.Header().Set("Content-Type", tc.contentType)
						}
						if tc.location != "" {
							w.Header().Set("Location", tc.location)
						}
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(tc.body))
					default:
						t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
					}
				}, func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.WriteHeader(http.StatusBadRequest)
				})
				configureDriveResponseSecretTestClient(env, verbose)

				args := []string{"drive", "upload", "file", src, "--space-id", "s1"}
				if verbose {
					args = append([]string{"--verbose"}, args...)
				}
				err := env.run(args...)
				if err == nil {
					t.Fatal("expected the multipart part upload and abort to fail")
				}
				assertNoResponseSecretVisible(t, env, err, marker)
				if got := abortCalls.Load(); got != 1 {
					t.Errorf("abort calls = %d, want 1", got)
				}
			})
		}
	}
}

func TestMultipartAbortRecoveryHintDoesNotInterpolateServerUploadID(t *testing.T) {
	tests := []struct {
		name     string
		uploadID string
		wantNote string
	}{
		{
			name:     "safe token",
			uploadID: "upload-1",
			wantNote: "Copy file_id and upload_id",
		},
		{
			name:     "double quote",
			uploadID: `upload"-quoted`,
			wantNote: "contains quoting characters",
		},
		{
			name:     "single quote",
			uploadID: `upload'-quoted`,
			wantNote: "contains quoting characters",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := multipartAbortRecoveryHint(tt.uploadID)
			t.Logf("upload_id=%q hint=%q", tt.uploadID, hint)
			if strings.Contains(hint, tt.uploadID) {
				t.Errorf("hint interpolated the server upload_id %q: %s", tt.uploadID, hint)
			}
			if !strings.Contains(hint, `<file_id>`) || !strings.Contains(hint, `<upload_id>`) {
				t.Errorf("hint must use file and upload placeholders: %s", hint)
			}
			if !strings.Contains(hint, tt.wantNote) {
				t.Errorf("hint %q does not contain %q", hint, tt.wantNote)
			}
		})
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
			if got := r.Header.Get(multipartCapabilityHeader); got != multipartCapabilityValue {
				t.Errorf("prepare capability header = %q, want %q", got, multipartCapabilityValue)
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
		t.Errorf("prepare calls = %d, want 1; a 413 prepare is not retried", prepareCalls)
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
			if got := r.Header.Get(multipartCapabilityHeader); got != multipartCapabilityValue {
				t.Errorf("prepare capability header = %q, want %q", got, multipartCapabilityValue)
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
