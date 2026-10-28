package index

import (
	"context"
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
	"github.com/darkearl/semantic-search/internal/testpdf"
)

func TestPipelineTokenlessPages(t *testing.T) {
	tok, err := embedding.ReadTokenizer(strings.NewReader("[PAD]\n[UNK]\n[CLS]\n[SEP]\n[MASK]\nwater\nsolar\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, options := range []Options{{1, 1}, {2, 2}} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("workers=%v/wrapped=%t", options, wrapped), func(t *testing.T) {
				var calls atomic.Int32
				model := embedFunc(func(_ context.Context, text string) ([]float32, error) {
					calls.Add(1)
					if len(tok.Windows(text)) == 0 {
						if wrapped {
							return nil, fmt.Errorf("normalize input: %w", embedding.ErrNoTokens)
						}
						return nil, embedding.ErrNoTokens
					}
					return unit(0), nil
				})
				checkTokenlessLibrary(t, model, options)
				if calls.Load() != 6 {
					t.Fatalf("got %d Embed calls; want one per non-whitespace page", calls.Load())
				}
			})
		}
	}
}

func TestTokenlessPDFIntegration(t *testing.T) {
	dir, library := os.Getenv("PDFSEARCH_TEST_MODEL_DIR"), os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if dir == "" || library == "" {
		t.Skip("set local model and runtime paths")
	}
	model, err := embedding.Open(dir, library, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	checkTokenlessLibrary(t, model, Options{2, 2})
}

func TestPipelineCancellationDuringTokenlessEmbedding(t *testing.T) {
	root := makeFiles(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := embedFunc(func(context.Context, string) ([]float32, error) {
		cancel()
		return nil, embedding.ErrNoTokens
	})
	idx, _, err := Build(ctx, root, "weights", true, nil, &fakePDF{pages: []string{"\u200b"}}, model, io.Discard)
	if idx != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("tokenless input hid cancellation: idx=%v err=%v", idx, err)
	}
}

func checkTokenlessLibrary(t *testing.T, model Embedder, options Options) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	// Map code 01 to U+200B while preserving ordinary ASCII text.
	const cmap = "1 begincodespacerange <00> <ff> endcodespacerange\n" +
		"1 beginbfrange <20> <7e> <0020> endbfrange\n" +
		"1 beginbfchar <01> <200b> endbfchar\n"
	fixtures := map[string][]string{
		"control.pdf": {"\x01"},
		"healthy.pdf": {"water"},
		"mixed.pdf":   {"\x01", "water", " ", "\x01", "solar"},
	}
	for name, pages := range fixtures {
		if err := testpdf.WriteWithCMap(filepath.Join(root, name), pages, cmap); err != nil {
			t.Fatal(err)
		}
	}
	extractor, err := pdf.New()
	if err != nil {
		t.Fatal(err)
	}
	text, err := extractor.PageText(ctx, filepath.Join(root, "control.pdf"), 1)
	extractor.Close()
	if err != nil || strings.TrimSpace(text) != "\u200b" {
		t.Fatalf("fixture must extract only U+200B: text=%q err=%v", text, err)
	}
	factory := func() (Extractor, error) { return pdf.New() }
	idx, stats, err := BuildConcurrent(ctx, root, "weights", true, nil, factory, model, io.Discard, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Documents) != 3 || stats.Indexed != 3 || stats.Pages != 3 || stats.EmptyPages != 4 || stats.Skipped != 0 || stats.Recovered != 0 || len(idx.Issues) != 0 {
		t.Fatalf("tokenless library: stats=%+v documents=%+v", stats, idx.Documents)
	}
	wantPages := map[string][]int{"control.pdf": {}, "healthy.pdf": {1}, "mixed.pdf": {2, 5}}
	for _, doc := range idx.Documents {
		if doc.PageCount != len(fixtures[doc.Filename]) {
			t.Fatalf("lost physical page count: %+v", doc)
		}
		numbers := []int{}
		for _, page := range doc.Pages {
			numbers = append(numbers, page.Number)
		}
		if !reflect.DeepEqual(numbers, wantPages[doc.Filename]) {
			t.Fatalf("%s: stored pages %v; want %v", doc.Filename, numbers, wantPages[doc.Filename])
		}
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(idx.Documents, loaded.Documents) {
		t.Fatal("saved index changed the stored pages or vectors")
	}
	results, err := loaded.Search(unit(0), 10, -1)
	if err != nil || len(results) != 3 {
		t.Fatalf("searchable pages=%v err=%v", results, err)
	}
	for _, result := range results {
		if result.Filename == "control.pdf" || result.Filename == "mixed.pdf" && result.Page != 2 && result.Page != 5 {
			t.Fatalf("tokenless page became searchable: %+v", result)
		}
	}
	noEmbedding := embedFunc(func(context.Context, string) ([]float32, error) {
		t.Error("unchanged documents should be reused without embedding")
		return nil, fmt.Errorf("unexpected embedding")
	})
	reused, stats, err := BuildConcurrent(ctx, root, "weights", true, loaded, factory, noEmbedding, io.Discard, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reused != 3 || stats.Indexed != 0 || stats.Pages != 3 || stats.EmptyPages != 4 || !reflect.DeepEqual(loaded.Documents, reused.Documents) {
		t.Fatalf("reuse lost empty pages: stats=%+v", stats)
	}
}
