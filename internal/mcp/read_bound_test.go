package mcp

import (
	"strings"
	"testing"
)

func TestReadBoundedBodyRejectsExtraByteAndKeepsExactBody(t *testing.T) {
	body, err := readBoundedBody(strings.NewReader("exact"), nil, 5)
	if err != nil || string(body) != "exact" {
		t.Fatalf("exact-length body: %q %v", body, err)
	}
	if _, err := readBoundedBody(strings.NewReader("excess"), nil, 5); err == nil {
		t.Fatal("body exceeded declared capacity without an error")
	}
	body, err = readBoundedBody(strings.NewReader("tail"), []byte("pre"), 7)
	if err != nil || string(body) != "pretail" {
		t.Fatalf("chunked prefix was not preserved: %q %v", body, err)
	}
}
