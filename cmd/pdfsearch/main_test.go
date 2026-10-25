package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkearl/semantic-search/internal/index"
	"github.com/darkearl/semantic-search/internal/testpdf"
)

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"unknown"}, {"index"}, {"search"}, {"search", "--top", "0", "topic"}, {"search", "--min-score", "NaN", "topic"}, {"download", "extra"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
}

// Opt-in test with real MiniLM weights and ONNX Runtime. No network is used.
func TestSemanticSearchIntegration(t *testing.T) {
	model := os.Getenv("PDFSEARCH_TEST_MODEL_DIR")
	library := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if model == "" || library == "" {
		t.Skip("set PDFSEARCH_TEST_MODEL_DIR and ONNXRUNTIME_SHARED_LIBRARY_PATH for real inference")
	}
	t.Setenv("PATH", "") // No pdfinfo/pdftotext or other helpers are available.
	root := t.TempDir()
	if err := testpdf.Write(filepath.Join(root, "handbook.pdf"), []string{
		"Baking sourdough bread requires flour, water, salt, and a starter.\nLet the dough rise before putting it in a hot oven.",
		"",
		"Photovoltaic panels convert sunlight into electricity.\nWind turbines generate clean power without burning fossil fuels.\nThese technologies reduce carbon emissions from electricity production.",
	}); err != nil {
		t.Fatal(err)
	}
	idxPath := filepath.Join(t.TempDir(), "search.index.json")
	common := []string{"--model-dir", model, "--runtime", library, "--threads", "1", "--index", idxPath}
	var out, log bytes.Buffer
	args := append([]string{"index", "--dir", root}, common...)
	if err := run(context.Background(), args, &out, &log); err != nil {
		t.Fatalf("index: %v\n%s", err, log.String())
	}
	if !strings.Contains(out.String(), "2 searchable pages, 1 empty pages skipped") {
		t.Fatalf("unexpected summary: %s", out.String())
	}
	out.Reset()
	args = append([]string{"search", "--json", "--top", "1"}, common...)
	args = append(args, "renewable energy sources")
	if err := run(context.Background(), args, &out, &log); err != nil {
		t.Fatal(err)
	}
	var results []index.Result
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Filename != "handbook.pdf" || results[0].Page != 3 {
		t.Fatalf("wrong semantic result: %s", out.String())
	}
	t.Logf("Paraphrased query matched: %s", strings.TrimSpace(out.String()))
	out.Reset()
	args = append([]string{"index", "--dir", root}, common...)
	if err := run(context.Background(), args, &out, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0 PDFs indexed, 1 reused") {
		t.Fatalf("incremental indexing: %s", out.String())
	}
}
