package index

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkearl/semantic-search/internal/embedding"
	"github.com/darkearl/semantic-search/internal/pdf"
)

func testDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum)
}

func TestPageTextHashesPersistAndFollowPhysicalPages(t *testing.T) {
	root := makeFiles(t, 2)
	texts := []string{"  alpha \n", "", " \t", "\u200b", "", "omega"}
	wantHashes := []string{testDigest(texts[0]), testDigest(texts[1]), testDigest(texts[2]), testDigest(texts[3]), "", testDigest(texts[5])}
	var closed, calls atomic.Int32
	factory := func() (Extractor, error) {
		return &pipelinePDF{count: len(texts), closed: &closed, text: func(_ context.Context, _ string, page int) (string, error) {
			if page == 5 {
				return "", errors.New("page extraction failed")
			}
			return texts[page-1], nil
		}}, nil
	}
	model := embedFunc(func(_ context.Context, text string) ([]float32, error) {
		calls.Add(1)
		if text == "\u200b" {
			return nil, embedding.ErrNoTokens
		}
		return unit(0), nil
	})
	build := func(old *Index) (*Index, Stats, error) {
		return BuildConcurrent(context.Background(), root, "weights", true, old, factory, model, io.Discard, nil, Options{2, 2})
	}
	idx, stats, err := build(nil)
	if err != nil || len(idx.Documents) != 2 || stats.Pages != 4 || stats.EmptyPages != 6 || stats.FailedPages != 2 || calls.Load() != 6 {
		t.Fatalf("initial index: stats=%+v calls=%d err=%v", stats, calls.Load(), err)
	}
	for _, doc := range idx.Documents {
		if doc.SHA256 != testDigest("pdf") || !reflect.DeepEqual(doc.PageTextSHA256, wantHashes) {
			t.Fatalf("wrong hashes for %s: file=%s pages=%v", doc.Filename, doc.SHA256, doc.PageTextSHA256)
		}
		if len(doc.Pages) != 2 || doc.Pages[0].Number != 1 || doc.Pages[1].Number != 6 {
			t.Fatalf("wrong searchable physical pages: %+v", doc.Pages)
		}
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || !reflect.DeepEqual(idx.Documents, loaded.Documents) {
		t.Fatalf("page hashes did not survive saving: %v", err)
	}
	reused, stats, err := build(loaded)
	if err != nil || stats.Reused != 2 || calls.Load() != 6 || !reflect.DeepEqual(loaded.Documents, reused.Documents) {
		t.Fatalf("unchanged index did not reuse hashes: stats=%+v calls=%d err=%v", stats, calls.Load(), err)
	}
	if err := os.WriteFile(filepath.Join(root, "00.pdf"), []byte("changed PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	updated, stats, err := build(reused)
	if err != nil || stats.Indexed != 1 || stats.Reused != 1 || calls.Load() != 6 {
		t.Fatalf("changed PDF was not reprocessed: stats=%+v calls=%d err=%v", stats, calls.Load(), err)
	}
	if updated.Documents[0].SHA256 != testDigest("changed PDF") || !reflect.DeepEqual(updated.Documents[0].PageTextSHA256, wantHashes) {
		t.Fatal("reprocessing lost the file or page hashes")
	}
}

func TestFormat8IndexRequiresRebuild(t *testing.T) {
	root := makeFiles(t, 1)
	idx, _, err := Build(context.Background(), root, "weights", true, nil, &fakePDF{pages: []string{"text"}}, &fakeModel{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	idx.Version = 8
	if err := idx.Validate(); err == nil {
		t.Fatal("accepted an older index")
	}
}

func TestPageTextHashValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hashes []string
		valid  bool
	}{
		{"missing hashes", nil, false},
		{"complete with failure", []string{testDigest("text"), "", testDigest("")}, true},
		{"missing page", []string{testDigest("text"), ""}, false},
		{"missing successful hash", []string{testDigest("text"), "", ""}, false},
		{"failed page has hash", []string{testDigest("text"), testDigest(""), testDigest("")}, false},
		{"nonhex hash", []string{strings.Repeat("z", 64), "", testDigest("")}, false},
		{"uppercase hash", []string{strings.Repeat("A", 64), "", testDigest("")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := PageFailure{Page: 2, Reason: "unreadable page"}
			idx := &Index{Version: Version, Root: t.TempDir(), Model: embedding.ModelID, Fingerprint: "weights",
				Documents: []Document{{Filename: "book.pdf", SHA256: testDigest("pdf"), PageCount: 3, Pages: []Page{{Number: 1, Vector: unit(0)}}, PageTextSHA256: tc.hashes, Failures: []PageFailure{failure}}},
				Issues:    []FileIssue{{Filename: "book.pdf", Kind: "partial", Reason: "unreadable page", IndexedPages: 1, Failures: []PageFailure{failure}}},
			}
			if err := idx.Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%t: %v", tc.valid, err)
			}
		})
	}
}

func TestSkippedPDFRetainsStableFileHash(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.pdf"), []byte("not a PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	idx, stats, err := BuildConcurrent(context.Background(), root, "weights", true, nil, func() (Extractor, error) { return pdf.New() }, &fakeModel{}, io.Discard, nil, Options{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 1 || len(idx.Issues) != 1 || idx.Issues[0].SHA256 != testDigest("not a PDF") {
		t.Fatalf("skipped PDF hash: stats=%+v issues=%+v", stats, idx.Issues)
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.Issues[0].SHA256 != testDigest("not a PDF") {
		t.Fatalf("skipped PDF hash did not survive saving: %v", err)
	}
}

type changedDuringOpen struct{}

func (changedDuringOpen) PageCount(_ context.Context, path string) (int, error) {
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		return 0, err
	}
	return 0, errors.New("document could not be opened")
}

func (changedDuringOpen) PageText(context.Context, string, int) (string, error) {
	panic("PageText should not be called after PageCount fails")
}

func TestSkippedPDFDoesNotRetainHashIfSourceChanges(t *testing.T) {
	root := makeFiles(t, 1)
	idx, stats, err := Build(context.Background(), root, "weights", true, nil, changedDuringOpen{}, &fakeModel{}, io.Discard)
	if err != nil || stats.Skipped != 1 || len(idx.Issues) != 1 || idx.Issues[0].SHA256 != "" || !strings.Contains(idx.Issues[0].Reason, "source changed during indexing") {
		t.Fatalf("changed source kept a hash: stats=%+v err=%v", stats, err)
	}
}
