package cmdutil

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseInput_Empty(t *testing.T) {
	f := NewTestFactory()
	got, err := ParseInput(f.Factory, "")
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if got != nil {
		t.Errorf("empty spec should return nil, got %v", got)
	}
}

func TestParseInput_RawString(t *testing.T) {
	f := NewTestFactory()
	got, err := ParseInput(f.Factory, `{"title":"hi"}`)
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if string(got) != `{"title":"hi"}` {
		t.Errorf("got %q", string(got))
	}
}

func TestParseInput_Stdin(t *testing.T) {
	f := NewTestFactory()
	f.In.WriteString(`{"from":"stdin"}`)
	got, err := ParseInput(f.Factory, "@-")
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if string(got) != `{"from":"stdin"}` {
		t.Errorf("got %q", string(got))
	}
}

func TestParseInput_StdinUnavailable(t *testing.T) {
	f := &Factory{}
	_, err := ParseInput(f, "@-")
	if err == nil {
		t.Error("expected error when stdin is nil")
	}
}

func TestParseInput_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.json")
	content := []byte(`{"from":"file"}`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	f := NewTestFactory()
	got, err := ParseInput(f.Factory, "@"+path)
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestParseInput_FileMissing(t *testing.T) {
	f := NewTestFactory()
	_, err := ParseInput(f.Factory, "@/nonexistent/nope.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error should reference read, got: %v", err)
	}
}

func TestParseInput_EmptyPath(t *testing.T) {
	f := NewTestFactory()
	_, err := ParseInput(f.Factory, "@")
	if err == nil {
		t.Error("expected error for empty file path")
	}
}
