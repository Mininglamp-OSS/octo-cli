package service

import (
	"net/http"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestDocsAttachmentsPresign_RequiresPositiveSizeBeforeHTTP(t *testing.T) {
	baseArgs := []string{"docs", "attachments", "presign", "d1"}
	fileFlags := []string{"--fileName", "report.png", "--mime", "image/png"}

	cases := []struct {
		name      string
		data      string
		flags     []string
		wantError bool
	}{
		{
			name:      "flag zero",
			flags:     append(append([]string{}, fileFlags...), "--sizeBytes", "0"),
			wantError: true,
		},
		{
			name:      "flag negative",
			flags:     append(append([]string{}, fileFlags...), "--sizeBytes=-1"),
			wantError: true,
		},
		{
			name:      "data zero",
			data:      `{"fileName":"report.png","mime":"image/png","sizeBytes":0}`,
			wantError: true,
		},
		{
			name:      "data negative merged with flags",
			data:      `{"sizeBytes":-1}`,
			flags:     fileFlags,
			wantError: true,
		},
		{
			name:      "zero flag overrides positive data",
			data:      `{"fileName":"report.png","mime":"image/png","sizeBytes":1}`,
			flags:     []string{"--sizeBytes", "0"},
			wantError: true,
		},
		{
			name:  "flag one",
			flags: append(append([]string{}, fileFlags...), "--sizeBytes", "1"),
		},
		{
			name: "data one",
			data: `{"fileName":"report.png","mime":"image/png","sizeBytes":1}`,
		},
		{
			name:  "data one merged with flags",
			data:  `{"sizeBytes":1}`,
			flags: fileFlags,
		},
		{
			name:  "one flag overrides zero data",
			data:  `{"fileName":"report.png","mime":"image/png","sizeBytes":0}`,
			flags: []string{"--sizeBytes", "1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			})

			args := append([]string{}, baseArgs...)
			if tc.data != "" {
				args = append(args, "--data", tc.data)
			}
			args = append(args, tc.flags...)
			root.SetArgs(args)
			err := root.Execute()

			if tc.wantError {
				exitErr := output.AsExitError(err)
				if exitErr == nil || exitErr.Code != "VALIDATION_ERROR" {
					t.Errorf("nonpositive attachment size error = %#v, want local VALIDATION_ERROR", exitErr)
				}
				if requests != 0 {
					t.Errorf("nonpositive attachment size sent %d requests, want 0", requests)
				}
				return
			}

			if err != nil {
				t.Fatalf("positive attachment size failed: %v", err)
			}
			if requests != 1 {
				t.Fatalf("positive attachment size sent %d requests, want 1", requests)
			}
		})
	}
}
