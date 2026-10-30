package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/darkearl/semantic-search/internal/embedding"
	"github.com/darkearl/semantic-search/internal/index"
	"github.com/darkearl/semantic-search/internal/pdf"
	"github.com/darkearl/semantic-search/internal/testpdf"
)

type checkTestModel struct{}

func (checkTestModel) Embed(context.Context, string) ([]float32, error) {
	v := make([]float32, embedding.Dimensions)
	v[0] = 1
	return v, nil
}
func TestCheckCommandNeedsNoModelOrRuntime(t *testing.T) {
	root := t.TempDir()
	if err := testpdf.Write(filepath.Join(root, "book.pdf"), []string{"text"}); err != nil {
		t.Fatal(err)
	}
	extractor, _ := pdf.New()
	defer extractor.Close()
	idx, _, err := index.Build(context.Background(), root, "test", true, nil, extractor, checkTestModel{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PDFSEARCH_MODEL_DIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("ONNXRUNTIME_SHARED_LIBRARY_PATH", "/missing-runtime")
	var out bytes.Buffer
	if err := run(context.Background(), []string{"check-index", "--index", path}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var result index.CheckResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ChangedPages != 0 || len(result.Errors) != 0 || len(result.Refresh) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
