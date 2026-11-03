package index

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildAndCheckRejectNonUTF8PDFPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames use Unicode")
	}
	root := t.TempDir()
	old, _, err := Build(context.Background(), root, "weights", true, nil, &fakePDF{pages: []string{"text"}}, &fakeModel{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b\xfe.pdf", "b\xff.pdf"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("pdf"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Build(context.Background(), root, "weights", true, old, &fakePDF{pages: []string{"text"}}, &fakeModel{}, io.Discard); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("build: %v", err)
	}
	if _, err := old.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("check: %v", err)
	}
}

func TestEmptyNonUTF8RootIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames use Unicode")
	}
	root := filepath.Join(t.TempDir(), "bad\xff")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Build(context.Background(), root, "weights", true, nil, &fakePDF{}, &fakeModel{}, io.Discard); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("build: %v", err)
	}
}
