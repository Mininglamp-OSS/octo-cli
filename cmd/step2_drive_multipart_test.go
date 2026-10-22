package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func parseMultipartNoRetryFlag(t *testing.T, env *driveTestEnv) {
	t.Helper()
	if err := env.root.PersistentFlags().Parse([]string{"--no-retry"}); err != nil {
		t.Fatalf("parse --no-retry: %v", err)
	}
}

func TestUploadOneMultipartPartNoRetrySkipsTransientAndSignatureRetries(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		wantPresigners int32
	}{
		{name: "transient response", status: http.StatusServiceUnavailable, wantPresigners: 0},
		{name: "rejected signature", status: http.StatusForbidden, wantPresigners: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var env *driveTestEnv
			var presignCalls, putCalls atomic.Int32
			env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/upload/parts-presign") {
					t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				presignCalls.Add(1)
				numbers := decodePartNumbers(t, r)
				parts := make([]map[string]any, 0, len(numbers))
				for _, number := range numbers {
					parts = append(parts, map[string]any{
						"part_number": number,
						"url":         fmt.Sprintf("%s/part/resigned", env.store.URL),
						"expires_at":  time.Now().Add(time.Hour).Format(time.RFC3339),
					})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"parts": parts})
			}, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				putCalls.Add(1)
				w.WriteHeader(tc.status)
			})
			parseMultipartNoRetryFlag(t, env)

			src := writeMultipartTestFile(t, []byte("part"))
			file, err := os.Open(src)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()

			cli, err := env.tf.Client()
			if err != nil {
				t.Fatalf("resolve client: %v", err)
			}
			storeClient := transferClient("part upload_url", true, nil, nil)
			t.Cleanup(storeClient.CloseIdleConnections)
			prepared := &prepareUploadResponse{
				FileID:             json.Number("7"),
				UploadID:           "upload-1",
				PartSize:           4,
				PartCount:          1,
				MaxConcurrentParts: 1,
			}
			part := multipartPart{
				number:    1,
				url:       env.store.URL + "/part/1",
				expiresAt: time.Now().Add(time.Hour),
			}
			_, err = uploadOneMultipartPart(
				context.Background(), env.tf.Factory, cli, storeClient,
				"/v1/bot/drive", file, 4, prepared, true, &part,
			)
			if err == nil {
				t.Fatal("part upload should fail on the first rejected PUT")
			}
			if got := putCalls.Load(); got != 1 {
				t.Errorf("part PUT requests = %d, want exactly the initial attempt", got)
			}
			if got := presignCalls.Load(); got != tc.wantPresigners {
				t.Errorf("part re-sign requests = %d, want %d", got, tc.wantPresigners)
			}
		})
	}
}

func TestCompleteMultipartUploadNoRetryMakesInitialAttemptOnly(t *testing.T) {
	var completeCalls atomic.Int32
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/upload/complete") {
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		completeCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal","message":"temporary failure"}`))
	}, nil)
	parseMultipartNoRetryFlag(t, env)

	cli, err := env.tf.Client()
	if err != nil {
		t.Fatalf("resolve client: %v", err)
	}
	_, err = completeMultipartUpload(
		context.Background(), cli, env.tf.Factory, "/v1/bot/drive", "7", "upload-1", []string{`"etag-1"`},
	)
	if err == nil {
		t.Fatal("completion should return the first definitive server failure")
	}
	if got := completeCalls.Load(); got != 1 {
		t.Errorf("complete requests = %d, want exactly the initial attempt", got)
	}
}
