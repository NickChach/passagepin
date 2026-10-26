package index

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkearl/semantic-search/internal/pdf"
	"github.com/darkearl/semantic-search/internal/testpdf"
)

func TestPartialIndexDiscardsFailedDocumentsAndPersistsReports(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"good.pdf", "recovered.pdf", "partial.pdf"} {
		if err := testpdf.Write(filepath.Join(root, name), []string{"first page", "second page", ""}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bad.pdf"), []byte("not a PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	recoveredPath := filepath.Join(root, "recovered.pdf")
	data, _ := os.ReadFile(recoveredPath)
	at := bytes.LastIndex(data, []byte("startxref"))
	if err := os.WriteFile(recoveredPath, append(data[:at], []byte("startxref\n0\n%%EOF\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	partialPath := filepath.Join(root, "partial.pdf")
	data, _ = os.ReadFile(partialPath)
	data = bytes.Replace(data, []byte("(second page) Tj"), []byte("(second page] Tj"), 1)
	if err := os.WriteFile(partialPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	factory := func() (Extractor, error) { return pdf.New() }
	model := embedFunc(func(context.Context, string) ([]float32, error) { return unit(0), nil })
	var events []Progress
	idx, stats, err := BuildConcurrent(context.Background(), root, "weights", true, nil, factory, model, io.Discard, func(p Progress) { events = append(events, p) }, Options{2, 2})
	if err != nil || stats.Indexed != 2 || stats.Recovered != 1 || stats.Skipped != 2 || stats.Pages != 4 || stats.EmptyPages != 2 || len(idx.Documents) != 2 {
		t.Fatalf("mixed library: stats=%+v err=%v", stats, err)
	}
	if len(idx.Issues) != 3 || idx.Issues[1].Filename != "partial.pdf" || idx.Issues[1].Page != 2 {
		t.Fatalf("incorrect issues: %+v", idx.Issues)
	}
	for _, event := range events {
		if event.Filename == "partial.pdf" && event.Phase == "completed" {
			t.Fatal("partial PDF was reported as indexed")
		}
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || len(loaded.Issues) != 3 || loaded.Documents[1].Recovery == "" {
		t.Fatalf("report did not survive reload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "good.pdf"), []byte("now unreadable"), 0600); err != nil {
		t.Fatal(err)
	}
	idx, stats, err = BuildConcurrent(context.Background(), root, "weights", true, loaded, factory, model, io.Discard, nil, Options{2, 2})
	if err != nil || stats.Reused != 1 || stats.Recovered != 1 || stats.Skipped != 3 || len(idx.Documents) != 1 || idx.Documents[0].Filename != "recovered.pdf" {
		t.Fatalf("stale vectors survived failed update: stats=%+v err=%v", stats, err)
	}
	if err := testpdf.Write(partialPath, []string{"fixed"}); err != nil {
		t.Fatal(err)
	}
	idx, stats, err = BuildConcurrent(context.Background(), root, "weights", true, idx, factory, model, io.Discard, nil, Options{2, 2})
	if err != nil || stats.Skipped != 2 || stats.Indexed != 1 {
		t.Fatalf("retry failed: stats=%+v err=%v", stats, err)
	}
	for _, issue := range idx.Issues {
		if issue.Filename == "partial.pdf" {
			t.Fatal("resolved failure remained in report")
		}
	}
}

func TestFatalModelAndCancellationErrorsStillAbort(t *testing.T) {
	root := t.TempDir()
	if err := testpdf.Write(filepath.Join(root, "book.pdf"), []string{"text"}); err != nil {
		t.Fatal(err)
	}
	modelErr := errors.New("model failed")
	factory := func() (Extractor, error) { return pdf.New() }
	for _, failure := range []error{modelErr, context.Canceled} {
		model := embedFunc(func(context.Context, string) ([]float32, error) { return nil, failure })
		idx, _, err := BuildConcurrent(context.Background(), root, "weights", true, nil, factory, model, io.Discard, nil, Options{2, 2})
		if idx != nil || !errors.Is(err, failure) {
			t.Fatalf("global error was skipped: %v", err)
		}
	}
}

func TestExternalLibraryPartialIndexing(t *testing.T) {
	root := os.Getenv("PDFSEARCH_TEST_LIBRARY_DIR")
	if root == "" {
		t.Skip("set PDFSEARCH_TEST_LIBRARY_DIR for extraction-only validation of a real library")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	model := embedFunc(func(context.Context, string) ([]float32, error) { return unit(0), nil })
	idx, stats, err := BuildConcurrent(ctx, root, "extraction-test", true, nil, func() (Extractor, error) { return pdf.New() }, model, io.Discard, nil, Options{2, 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(filepath.Join(t.TempDir(), "index.json")); err != nil {
		t.Fatal(err)
	}
	t.Logf("Extraction-only result: indexed=%d recovered=%d skipped=%d searchable pages=%d", stats.Indexed, stats.Recovered, stats.Skipped, stats.Pages)
	for _, issue := range stats.Issues {
		if strings.Contains(issue.Reason, "context canceled") {
			t.Fatal("cancelled work was silently skipped")
		}
		t.Logf("%s: %s (page %d): %s", issue.Kind, issue.Filename, issue.Page, issue.Reason)
	}
}
