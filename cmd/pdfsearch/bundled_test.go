//go:build bundled

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

func TestBundledOfflineSearchWithoutExternalAssets(t *testing.T) {
	for _, key := range []string{"PDFSEARCH_MODEL_DIR", "ONNXRUNTIME_SHARED_LIBRARY_PATH", "LD_LIBRARY_PATH", "PATH"} {
		t.Setenv(key, "")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	root := t.TempDir()
	if err := testpdf.Write(filepath.Join(root, "topics.pdf"), []string{
		"Sourdough bread needs flour, salt, water and a starter.", "",
		"Photovoltaic panels and wind turbines produce clean electricity without burning fossil fuels.",
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "library.index.json")
	var out, log bytes.Buffer
	if err := run(context.Background(), []string{"index", "--dir", root, "--index", path, "--threads", "1"}, &out, &log); err != nil {
		t.Fatalf("bundled index: %v\n%s", err, log.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"search", "--index", path, "--threads", "1", "--json", "--top", "1", "renewable energy sources"}, &out, &log); err != nil {
		t.Fatal(err)
	}
	var results []index.Result
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Page != 3 || results[0].Filename != "topics.pdf" {
		t.Fatalf("wrong bundled result: %s", out.String())
	}
	if err := filepath.WalkDir(cache, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".onnx") || strings.HasSuffix(path, "vocab.txt") {
			t.Errorf("model asset was unnecessarily extracted: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("Offline bundled result: %s", strings.TrimSpace(out.String()))
}
