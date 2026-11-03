package index

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/darkearl/semantic-search/internal/pdf"
	"github.com/darkearl/semantic-search/internal/testpdf"
)

func TestLegacyReaderIndexUpdatesUnchangedFilesAndReusesMatchingPages(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := testpdf.Write(filepath.Join(root, "book.pdf"), []string{"corrected text", "stable text"}); err != nil {
		t.Fatal(err)
	}
	// Model a successful older extraction of the exact same PDF bytes. Page 1
	// was decoded incorrectly; page 2 should keep its existing embedding.
	legacy, _, err := Build(ctx, root, "weights", true, nil, &fakePDF{pages: []string{"incorrect text", "stable text"}}, &fakeModel{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	legacy.ExtractionRevision = 0
	path := filepath.Join(t.TempDir(), "index.json")
	if err := legacy.Save(path); err != nil {
		t.Fatal(err)
	}
	old, err := Load(path) // The omitted revision in existing format-9 files remains readable.
	if err != nil {
		t.Fatal(err)
	}
	check, err := old.Check(ctx)
	if err != nil || len(check.Errors) != 1 || len(check.Refresh) != 0 {
		t.Fatalf("legacy reader must prompt for an update: %+v %v", check, err)
	}
	var calls atomic.Int32
	model := embedFunc(func(_ context.Context, text string) ([]float32, error) {
		calls.Add(1)
		if text != "corrected text" {
			return nil, fmt.Errorf("re-embedded unchanged page text: %q", text)
		}
		return unit(1), nil
	})
	updated, stats, err := BuildConcurrent(ctx, root, "weights", true, old, func() (Extractor, error) { return pdf.New() }, model, io.Discard, nil, Options{2, 2})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || stats.Reused != 0 || stats.Indexed != 1 || updated.ExtractionRevision != pdf.ExtractionRevision {
		t.Fatalf("migration: calls=%d stats=%+v revision=%d", calls.Load(), stats, updated.ExtractionRevision)
	}
	before, after := old.Documents[0], updated.Documents[0]
	if before.SHA256 != after.SHA256 || before.PageTextSHA256[0] == after.PageTextSHA256[0] || !reflect.DeepEqual(after.Pages[0].Vector, unit(1)) || !reflect.DeepEqual(before.Pages[1], after.Pages[1]) {
		t.Fatal("migration did not replace only the corrected page")
	}
	if old.ExtractionRevision != 0 || old.Documents[0].PageTextSHA256[0] != testDigest("incorrect text") {
		t.Fatal("migration mutated the old snapshot")
	}
	if err := updated.Save(path); err != nil {
		t.Fatal(err)
	}
	updated, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	check, err = updated.Check(ctx)
	if err != nil || len(check.Errors) != 0 || len(check.Refresh) != 0 {
		t.Fatalf("updated reader should be current: %+v %v", check, err)
	}
	_, stats, err = Build(ctx, root, "weights", true, updated, &fakePDF{fail: true}, model, io.Discard)
	if err != nil || stats.Reused != 1 || calls.Load() != 1 {
		t.Fatalf("repeat update did not reuse the migrated file: %+v %v", stats, err)
	}
}

func TestInvalidReaderRevisionsAreRejected(t *testing.T) {
	idx, _, err := Build(context.Background(), makeFiles(t, 1), "weights", true, nil, &fakePDF{pages: []string{"text"}}, &fakeModel{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []int{-1, pdf.ExtractionRevision + 1} {
		idx.ExtractionRevision = revision
		if err := idx.Validate(); err == nil {
			t.Fatalf("accepted reader revision %d", revision)
		}
	}
}
