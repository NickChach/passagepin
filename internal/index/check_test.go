package index

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkearl/semantic-search/internal/pdf"
	"github.com/darkearl/semantic-search/internal/testpdf"
)

func TestCheckAndIncrementalPageReplacement(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "book.pdf")
	write := func(pages []string) {
		t.Helper()
		if err := testpdf.Write(path, pages); err != nil {
			t.Fatal(err)
		}
	}
	build := func(old *Index, model *fakeModel) *Index {
		t.Helper()
		e, _ := pdf.New()
		defer e.Close()
		idx, _, err := Build(ctx, root, "weights", true, old, e, model, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		return idx
	}
	write([]string{"alpha", "beta", ""})
	model := &fakeModel{}
	idx := build(nil, model)
	if model.calls != 2 {
		t.Fatal(model.calls)
	}
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(data, []byte("\n% saved annotation\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := idx.Check(ctx)
	if err != nil || result.ChangedPages != 0 || len(result.Refresh) != 1 || len(result.Errors) != 0 {
		t.Fatalf("annotation check: %+v %v", result, err)
	}
	before := idx.Documents[0].SHA256
	refreshed, err := idx.RefreshFiles(result.Refresh)
	if err != nil || refreshed.Documents[0].SHA256 == before || idx.Documents[0].SHA256 != before {
		t.Fatalf("refresh mutated snapshot: %v", err)
	}
	if _, err := refreshed.RefreshFiles(result.Refresh); err == nil {
		t.Fatal("accepted stale refresh")
	}
	result, err = refreshed.Check(ctx)
	if err != nil || len(result.Refresh) != 0 {
		t.Fatalf("repeat check: %+v %v", result, err)
	}
	write([]string{"alpha", "gamma", "", "delta"})
	result, err = refreshed.Check(ctx)
	if err != nil || result.ChangedPages != 2 || len(result.Refresh) != 0 {
		t.Fatalf("changed pages: %+v %v", result, err)
	}
	updated := build(refreshed, model)
	if model.calls != 4 || len(updated.Documents[0].Pages) != 3 {
		t.Fatalf("unchanged page embedded again: %d", model.calls)
	}
	write([]string{"alpha", ""})
	updated = build(updated, model)
	if model.calls != 4 || len(updated.Documents[0].Pages) != 1 || updated.Documents[0].PageCount != 2 {
		t.Fatal("removed or emptied pages kept vectors")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := testpdf.Write(filepath.Join(root, "new.pdf"), []string{"new"}); err != nil {
		t.Fatal(err)
	}
	result, err = updated.Check(ctx)
	if err != nil || result.AddedFiles != 1 || result.RemovedFiles != 1 {
		t.Fatalf("file changes: %+v %v", result, err)
	}
}

func TestCheckReportsUnreadableChangedPDFAndCancellation(t *testing.T) {
	ctx := context.Background()
	root := makeFiles(t, 1)
	idx, _, err := Build(ctx, root, "weights", true, nil, &fakePDF{pages: []string{"text"}}, &fakeModel{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "00.pdf"), []byte("broken changed file"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := idx.Check(ctx)
	if err != nil || len(result.Errors) != 1 || len(result.Refresh) != 0 {
		t.Fatalf("failure lost: %+v %v", result, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := idx.Check(cancelled); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestCheckPreservesNonRecursiveScopeAcrossReloadAndUpdate(t *testing.T) {
	root := makeFiles(t, 1)
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "excluded.pdf"), []byte("pdf"), 0600); err != nil {
		t.Fatal(err)
	}
	build := func(old *Index) *Index {
		t.Helper()
		idx, _, err := Build(context.Background(), root, "weights", false, old, &fakePDF{pages: []string{"text"}}, &fakeModel{}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		return idx
	}
	idx := build(nil)
	indexPath := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(indexPath); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []*Index{loaded, build(loaded)} {
		r, err := snapshot.Check(context.Background())
		if err != nil || r.AddedFiles != 0 || r.RemovedFiles != 0 {
			t.Fatalf("excluded file reported: %+v %v", r, err)
		}
	}
	// Legacy format-9 indexes retain the previous recursive default. An update
	// with the original CLI option records their scope without a rebuild.
	loaded.NonRecursive = false
	r, err := loaded.Check(context.Background())
	if err != nil || r.AddedFiles != 1 {
		t.Fatalf("recursive default: %+v %v", r, err)
	}
	r, err = build(loaded).Check(context.Background())
	if err != nil || r.AddedFiles != 0 {
		t.Fatalf("scope migration: %+v %v", r, err)
	}
}

type controlledCheckReader struct {
	fakePDF
	read   func(context.Context, string, int) (string, error)
	closed bool
}

func (r *controlledCheckReader) PageText(ctx context.Context, path string, page int) (string, error) {
	return r.read(ctx, path, page)
}
func (r *controlledCheckReader) Close() error { r.closed = true; return nil }

func TestCheckerReaderFailuresAndCancellationReleaseReader(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			root := makeFiles(t, 1)
			idx, _, err := Build(context.Background(), root, "weights", true, nil, &fakePDF{pages: []string{"text"}}, &fakeModel{}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "00.pdf"), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader := &controlledCheckReader{fakePDF: fakePDF{pages: []string{"text"}}}
			reader.read = func(context.Context, string, int) (string, error) {
				if cancelled {
					cancel()
					return "", ctx.Err()
				}
				return "", errors.New("controlled page failure")
			}
			result, err := idx.check(ctx, func() (checkExtractor, error) { return reader, nil })
			if !reader.closed || len(result.Refresh) != 0 {
				t.Fatalf("reader cleanup or unsafe refresh: %+v", result)
			}
			if cancelled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			} else if err != nil || len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "controlled page failure") {
				t.Fatalf("page failure lost: %+v %v", result, err)
			}
		})
	}
}
