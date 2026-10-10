package service

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestPinnedMultipartCopyBoundsGrowingFile(t *testing.T) {
	for _, size := range []int{8, 9, 32} {
		var out bytes.Buffer
		err := copyMultipartFile(&out, strings.NewReader(strings.Repeat("x", size)), true, 8)
		if (err != nil) != (size > 8) {
			t.Fatalf("size=%d err=%v", size, err)
		}
		if out.Len() > 9 {
			t.Fatalf("unbounded allocation: %d", out.Len())
		}
	}
	var out bytes.Buffer
	if err := copyMultipartFile(&out, strings.NewReader(strings.Repeat("x", 32)), false, 8); err != nil || out.Len() != 32 {
		t.Fatalf("ordinary CLI changed: %v %d", err, out.Len())
	}
}

func TestPinnedMultipartReadsDescriptorAfterGrowth(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "upload")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("12345678"); err != nil {
		t.Fatal(err)
	}
	stat, err := f.Stat()
	if err != nil || stat.Size() != 8 {
		t.Fatal(stat, err)
	}
	if _, err := f.WriteString("9"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := copyMultipartFile(&out, f, true, 8); err == nil {
		t.Fatal("growth after stat accepted")
	}
	if out.Len() != 9 {
		t.Fatal(out.Len())
	}
}
