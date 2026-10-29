package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePDFDoesNotDependOnTextExtraction(t *testing.T) {
	root, indexPath := library(t)
	engine := New(&fakeModel{}, "weights")
	ctx := context.Background()
	if _, err := engine.Open(ctx, OpenParams{Directory: root, IndexPath: indexPath}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.BuildIndex(ctx, IndexParams{}, nil); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "topics.pdf")
	// The source may become unreadable to our extractor after indexing. Opening
	// it in an external viewer must still be possible without decoding the page.
	if err := os.WriteFile(file, []byte("damaged PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	location, err := engine.ResolvePDF(ctx, PageParams{Filename: "topics.pdf", Page: 1})
	if err != nil || location.Path != file {
		t.Fatalf("resolve: %+v %v", location, err)
	}
	if _, err := engine.Page(ctx, PageParams{Filename: "topics.pdf", Page: 1}); err == nil {
		t.Fatal("expected preview extraction to fail")
	}
	if _, err := engine.Handle(ctx, request("resolve-pdf", PageParams{Filename: "topics.pdf", Page: 1}), nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []PageParams{{"../topics.pdf", 1}, {"missing.pdf", 1}, {"topics.pdf", 0}, {"topics.pdf", 4}} {
		if _, err := engine.ResolvePDF(ctx, p); err == nil {
			t.Fatalf("accepted invalid location: %+v", p)
		}
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.pdf")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ResolvePDF(ctx, PageParams{"topics.pdf", 1}); err == nil {
		t.Fatal("accepted symlink outside library")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ResolvePDF(ctx, PageParams{"topics.pdf", 1}); err == nil {
		t.Fatal("accepted directory as PDF")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := engine.ResolvePDF(cancelled, PageParams{"topics.pdf", 1}); err != context.Canceled {
		t.Fatalf("lost cancellation: %v", err)
	}
}
