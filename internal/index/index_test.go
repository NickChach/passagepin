package index

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkearl/semantic-search/internal/embedding"
)

type fakePDF struct {
	pages []string
	calls []int
	fail  bool
}

func (f *fakePDF) PageCount(context.Context, string) (int, error) { return len(f.pages), nil }
func (f *fakePDF) PageText(_ context.Context, _ string, page int) (string, error) {
	f.calls = append(f.calls, page)
	if f.fail {
		return "", errors.New("broken PDF")
	}
	return f.pages[page-1], nil
}

type fakeModel struct{ calls int }

func unit(axis int) []float32                                         { v := make([]float32, embedding.Dimensions); v[axis] = 1; return v }
func (f *fakeModel) Embed(context.Context, string) ([]float32, error) { f.calls++; return unit(0), nil }

func TestPreviousExtractorIndexNeedsRebuild(t *testing.T) {
	for _, version := range []int{2, 3, 4} {
		idx := &Index{Version: version, Root: t.TempDir(), Model: embedding.ModelID, Fingerprint: "weights"}
		if err := idx.Validate(); err == nil || !strings.Contains(err.Error(), "rebuild") {
			t.Fatalf("accepted vectors from extractor version %d: %v", version, err)
		}
	}
}

func TestIncrementalIndexPageNumbersAndDeletion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "book.PDF")
	if err := os.WriteFile(path, []byte("v1"), 0600); err != nil {
		t.Fatal(err)
	}
	extractor := &fakePDF{pages: []string{"garden", " ", "solar power"}}
	model := &fakeModel{}
	idx, stats, err := Build(ctx, root, "weights", true, nil, extractor, model, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Indexed != 1 || stats.Pages != 2 || stats.EmptyPages != 1 || model.calls != 2 {
		t.Fatalf("stats=%+v calls=%d", stats, model.calls)
	}
	if idx.Documents[0].Pages[1].Number != 3 {
		t.Fatal("blank page shifted numbering")
	}
	saved := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(saved); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(saved)
	if err != nil {
		t.Fatal(err)
	}
	idx, stats, err = Build(ctx, root, "weights", true, loaded, extractor, model, io.Discard)
	if err != nil || stats.Reused != 1 || model.calls != 2 || len(extractor.calls) != 3 {
		t.Fatalf("reuse stats=%+v err=%v", stats, err)
	}
	if err := os.WriteFile(path, []byte("v2"), 0600); err != nil {
		t.Fatal(err)
	}
	idx, stats, err = Build(ctx, root, "weights", true, idx, extractor, model, io.Discard)
	if err != nil || stats.Indexed != 1 || model.calls != 4 {
		t.Fatalf("changed stats=%+v err=%v", stats, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	idx, _, err = Build(ctx, root, "weights", true, idx, extractor, model, io.Discard)
	if err != nil || len(idx.Documents) != 0 {
		t.Fatalf("deleted PDF retained: %v", err)
	}
}

func TestSkippedDocumentBuildDoesNotWriteSavedIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "book.pdf"), []byte("v1"), 0600); err != nil {
		t.Fatal(err)
	}
	e := &fakePDF{pages: []string{"text"}}
	idx, _, err := Build(context.Background(), root, "weights", true, nil, e, &fakeModel{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	e.fail = true
	if candidate, stats, err := Build(context.Background(), root, "weights", true, nil, e, &fakeModel{}, io.Discard); err != nil || stats.Skipped != 1 || len(candidate.Documents) != 0 {
		t.Fatalf("expected the unreadable PDF to be excluded: skipped=%d err=%v", stats.Skipped, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("building the candidate overwrote the saved index")
	}
	if _, _, err := Build(context.Background(), root, "other-weights", true, idx, e, &fakeModel{}, io.Discard); err == nil {
		t.Fatal("allowed incompatible model reuse")
	}
}

func TestSearchRankingThresholdAndInvalidVectors(t *testing.T) {
	idx := &Index{Documents: []Document{
		{Filename: "z.pdf", Pages: []Page{{Number: 2, Vector: unit(0)}, {Number: 3, Vector: unit(1)}}},
		{Filename: "a.pdf", Pages: []Page{{Number: 5, Vector: unit(0)}}},
	}}
	results, err := idx.Search(unit(0), 2, 0.5)
	if err != nil || len(results) != 2 || results[0].Filename != "a.pdf" || results[1].Page != 2 || results[0].Score != 1 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	negative := unit(0)
	negative[0] = -1
	results, err = idx.Search(negative, 10, 0.1)
	if err != nil || len(results) != 0 {
		t.Fatalf("threshold failed: %+v %v", results, err)
	}
	for _, bad := range [][]float32{nil, make([]float32, embedding.Dimensions), func() []float32 { v := unit(0); v[0] = float32(math.NaN()); return v }()} {
		if _, err := idx.Search(bad, 10, 0); err == nil {
			t.Fatal("accepted invalid query vector")
		}
	}
}

func TestRecursiveAndSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "book.pdf"), []byte("pdf"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "sub", "book.pdf"), filepath.Join(root, "linked.pdf")); err != nil {
		t.Skip(err)
	}
	for _, recursive := range []bool{false, true} {
		idx, _, err := Build(context.Background(), root, "weights", recursive, nil, &fakePDF{pages: []string{"text"}}, &fakeModel{}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if recursive {
			want = 1
		}
		if len(idx.Documents) != want {
			t.Fatalf("recursive=%v: got %d docs", recursive, len(idx.Documents))
		}
	}
}
