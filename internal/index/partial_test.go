package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/darkearl/semantic-search/internal/pdf"
	"github.com/darkearl/semantic-search/internal/testpdf"
)

func TestCorpusIndexReportsAndReplacementText(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"actualtext-inline.pdf", "image-only.pdf", "inherited-tounicode.pdf", "late-page-failure.pdf"} {
		data, err := os.ReadFile(filepath.Join("../pdf/testdata/corpus", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var embedded sync.Map
	model := embedFunc(func(_ context.Context, text string) ([]float32, error) {
		embedded.Store(text, true)
		return unit(0), nil
	})
	var log bytes.Buffer
	idx, stats, err := BuildConcurrent(context.Background(), root, "weights", true, nil, func() (Extractor, error) { return pdf.New() }, model, &log, nil, Options{2, 2})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Indexed != 3 || stats.Skipped != 1 || stats.Partial != 1 || stats.FailedPages != 2 || stats.Pages != 2 || stats.EmptyPages != 1 || len(idx.Documents) != 3 {
		t.Fatalf("corpus stats: %+v", stats)
	}
	if _, ok := embedded.Load("office work"); !ok {
		t.Fatal("replacement text did not reach the embedder")
	}
	for _, issue := range idx.Issues {
		wantPage, wantFeature := 1, "tounicode-inheritance"
		wantKind := "skipped"
		if issue.Filename == "late-page-failure.pdf" {
			wantPage, wantFeature = 2, "stream-filter"
			wantKind = "partial"
		}
		if issue.Kind != wantKind || issue.Page != wantPage || len(issue.Failures) != 1 || issue.Failures[0].Feature != wantFeature {
			t.Fatalf("wrong report scope/feature: %+v", issue)
		}
	}
	if !strings.Contains(log.String(), "Partially indexed: late-page-failure.pdf; 1 searchable pages; 1 pages failed extraction") || !strings.Contains(log.String(), "no pages from this PDF were indexed") {
		t.Fatalf("ambiguous failure log: %s", log.String())
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || !reflect.DeepEqual(loaded.Issues, idx.Issues) {
		t.Fatalf("lost feature diagnostics on reload: %v", err)
	}
}

func TestRecoveryRetryFailureDiscardsWholeDocumentOnUpdate(t *testing.T) {
	for _, workers := range []int{1, 2} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			root := t.TempDir()
			broken := filepath.Join(root, "broken.pdf")
			if err := testpdf.Write(broken, []string{"first page", "second page"}); err != nil {
				t.Fatal(err)
			}
			if err := testpdf.Write(filepath.Join(root, "healthy.pdf"), []string{"", "healthy page"}); err != nil {
				t.Fatal(err)
			}
			factory := func() (Extractor, error) { return pdf.New() }
			model := embedFunc(func(context.Context, string) ([]float32, error) { return unit(0), nil })
			build := func(old *Index) (*Index, Stats, error) {
				return BuildConcurrent(context.Background(), root, "weights", true, old, factory, model, io.Discard, nil, Options{workers, workers})
			}
			old, stats, err := build(nil)
			if err != nil || stats.Pages != 3 {
				t.Fatalf("initial index: %+v, %v", stats, err)
			}
			data, err := os.ReadFile(broken)
			if err != nil {
				t.Fatal(err)
			}
			// Only page 2 references missing object 9. Keep byte lengths and
			// all xref offsets intact, so the first recovery occurs on that page.
			at := bytes.LastIndex(data, []byte("/F1 3 0 R"))
			if at < 0 {
				t.Fatal("missing fixture font resource")
			}
			copy(data[at:], []byte("/F1 9 0 R"))
			if err := os.WriteFile(broken, data, 0600); err != nil {
				t.Fatal(err)
			}
			idx, stats, err := build(old)
			if err != nil || stats.Reused != 1 || stats.Skipped != 1 || stats.Partial != 0 || stats.Recovered != 0 || stats.Pages != 1 || stats.EmptyPages != 1 || len(idx.Documents) != 1 {
				t.Fatalf("unsafe vectors survived update: %+v, %v", stats, err)
			}
			if idx.Documents[0].Filename != "healthy.pdf" || idx.Documents[0].Pages[0].Number != 2 {
				t.Fatalf("lost healthy PDF or physical page number: %+v", idx.Documents)
			}
			if len(idx.Issues) != 1 || idx.Issues[0].Kind != "skipped" || idx.Issues[0].Page != 2 || !strings.Contains(idx.Issues[0].Reason, "missing object 9") {
				t.Fatalf("wrong structural failure report: %+v", idx.Issues)
			}
			path := filepath.Join(t.TempDir(), "index.json")
			if err := idx.Save(path); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			results, err := loaded.Search(unit(0), 10, -1)
			if err != nil || len(results) != 1 || results[0].Filename != "healthy.pdf" || results[0].Page != 2 {
				t.Fatalf("unsafe saved search results: %+v, %v", results, err)
			}
			retried, stats, err := build(loaded)
			if err != nil || stats.Skipped != 1 || stats.Partial != 0 || !reflect.DeepEqual(retried.Issues, loaded.Issues) {
				t.Fatalf("skipped document was not retried: %+v, %v", stats, err)
			}
			after, err := os.ReadFile(broken)
			if err != nil || !bytes.Equal(data, after) {
				t.Fatalf("recovery modified source PDF: %v", err)
			}
		})
	}
}

func TestPartialIndexRetainsHealthyPagesAndPersistsReports(t *testing.T) {
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
	if err != nil || stats.Indexed != 3 || stats.Recovered != 1 || stats.Skipped != 1 || stats.Partial != 1 || stats.FailedPages != 1 || stats.Pages != 5 || stats.EmptyPages != 3 || len(idx.Documents) != 3 {
		t.Fatalf("mixed library: stats=%+v err=%v", stats, err)
	}
	if len(idx.Issues) != 3 || idx.Issues[1].Filename != "partial.pdf" || idx.Issues[1].Page != 2 {
		t.Fatalf("incorrect issues: %+v", idx.Issues)
	}
	completedPartial := false
	for _, event := range events {
		if event.Filename == "partial.pdf" && event.Phase == "completed" && event.Partial {
			completedPartial = true
		}
	}
	if !completedPartial {
		t.Fatal("partial PDF completion did not identify retained pages")
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || len(loaded.Issues) != 3 || loaded.Documents[2].Recovery == "" || !reflect.DeepEqual(idx.Documents, loaded.Documents) {
		t.Fatalf("report did not survive reload: %v", err)
	}
	wantCounts := Counts{Documents: 3, Pages: 5, EmptyPages: 3, FailedPages: 1, Recovered: 1, Partial: 1, Skipped: 1}
	if idx.Counts() != wantCounts || loaded.Counts() != wantCounts {
		t.Fatalf("snapshot counts changed on reload: build=%+v loaded=%+v", idx.Counts(), loaded.Counts())
	}
	if err := os.WriteFile(filepath.Join(root, "good.pdf"), []byte("now unreadable"), 0600); err != nil {
		t.Fatal(err)
	}
	idx, stats, err = BuildConcurrent(context.Background(), root, "weights", true, loaded, factory, model, io.Discard, nil, Options{2, 2})
	if err != nil || stats.Reused != 2 || stats.Recovered != 1 || stats.Skipped != 2 || stats.Partial != 1 || stats.FailedPages != 1 || stats.EmptyPages != 2 || len(idx.Documents) != 2 || idx.Documents[0].Filename != "partial.pdf" {
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

func TestPageFailuresPreserveLaterPhysicalPageNumbers(t *testing.T) {
	root := makeFiles(t, 1)
	factory := func() (Extractor, error) {
		return &pipelinePDF{count: 6, closed: &atomic.Int32{}, text: func(_ context.Context, _ string, page int) (string, error) {
			switch page {
			case 1, 3:
				return "", &pdf.UnsupportedFeatureError{Feature: "missing-unicode-map", Detail: "unmapped glyph"}
			case 5:
				return "", nil
			default:
				return "readable", nil
			}
		}}, nil
	}
	model := embedFunc(func(context.Context, string) ([]float32, error) { return unit(0), nil })
	idx, stats, err := BuildConcurrent(context.Background(), root, "weights", true, nil, factory, model, io.Discard, nil, Options{2, 2})
	if err != nil || stats.Indexed != 1 || stats.Partial != 1 || stats.Skipped != 0 || stats.Pages != 3 || stats.EmptyPages != 1 || stats.FailedPages != 2 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	var numbers []int
	for _, page := range idx.Documents[0].Pages {
		numbers = append(numbers, page.Number)
	}
	if !reflect.DeepEqual(numbers, []int{2, 4, 6}) {
		t.Fatalf("page numbers shifted: %v", numbers)
	}
	if len(idx.Issues) != 1 || idx.Issues[0].IndexedPages != 3 || len(idx.Issues[0].Failures) != 2 || idx.Issues[0].Failures[1].Page != 3 {
		t.Fatalf("lost failure details: %+v", idx.Issues)
	}
	if err := idx.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPageResourceAndStructuralFailuresDoNotKeepUnsafeVectors(t *testing.T) {
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded, syscall.EMFILE, syscall.ENFILE, syscall.ENOMEM, syscall.EIO, &pdf.DocumentError{Err: errors.New("recovery changed page structure")}} {
		t.Run(failure.Error(), func(t *testing.T) {
			factory := func() (Extractor, error) {
				return &pipelinePDF{count: 3, closed: &atomic.Int32{}, text: func(_ context.Context, _ string, page int) (string, error) {
					if page == 2 {
						return "", failure
					}
					if page == 3 {
						t.Error("continued after a document/resource failure")
					}
					return "readable", nil
				}}, nil
			}
			model := embedFunc(func(context.Context, string) ([]float32, error) { return unit(0), nil })
			idx, stats, err := BuildConcurrent(context.Background(), makeFiles(t, 1), "weights", true, nil, factory, model, io.Discard, nil, Options{1, 1})
			var structural *pdf.DocumentError
			if errors.As(failure, &structural) {
				if err != nil || len(idx.Documents) != 0 || stats.Skipped != 1 || stats.Partial != 0 || stats.Pages != 0 || stats.FailedPages != 1 {
					t.Fatalf("retained unsafe document: %+v, %v", stats, err)
				}
			} else if idx != nil || !errors.Is(err, failure) {
				t.Fatalf("resource failure was downgraded: %v", err)
			}
		})
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
	t.Logf("Partial documents=%d; failed pages=%d; empty pages=%d", stats.Partial, stats.FailedPages, stats.EmptyPages)
	for _, issue := range stats.Issues {
		if strings.Contains(issue.Reason, "context canceled") {
			t.Fatal("cancelled work was silently skipped")
		}
		t.Logf("%s: %s (page %d): %s", issue.Kind, issue.Filename, issue.Page, issue.Reason)
		for _, failure := range issue.Failures {
			t.Logf("  physical page %d: %s", failure.Page, failure.Reason)
		}
	}
}
