package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkearl/semantic-search/internal/index"
	"github.com/darkearl/semantic-search/internal/testpdf"
)

// Verify the shipped artifact, copied away from the source checkout, in fresh
// processes with no model/runtime variables, PATH, or external model files.
func TestReleaseExecutable(t *testing.T) {
	source := os.Getenv("PDFSEARCH_TEST_BUNDLE_BINARY")
	if source == "" {
		t.Skip("set PDFSEARCH_TEST_BUNDLE_BINARY to test a built release")
	}
	work := t.TempDir()
	binary := filepath.Join(work, "pdfsearch")
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		in.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	in.Close()
	closeErr := out.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	root := filepath.Join(work, "pdfs")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := testpdf.Write(filepath.Join(root, "handbook.pdf"), []string{
		"Baking sourdough bread uses flour, water, salt and a starter.", "",
		"Solar panels and wind turbines produce clean electricity without burning fossil fuels.",
	}); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(work, "cache")
	runRelease := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = work
		cmd.Env = []string{"HOME=" + work, "XDG_CACHE_HOME=" + cache, "PATH=", "LANG=C"}
		// stderr is separate so JSON on stdout remains machine-readable.
		var stderr captureWriter
		cmd.Stderr = &stderr
		data, err := cmd.Output()
		if err != nil {
			t.Fatalf("release %v: %v\n%s", args, err, string(stderr))
		}
		return data
	}
	runRelease("bundle-info")
	runRelease("licenses")
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("metadata commands initialized a runtime cache")
	}
	runRelease("index", "--dir", root, "--threads", "1")
	query := []string{"search", "--threads", "1", "--json", "--top", "1", "renewable energy sources"}
	checkResult := func(raw []byte) {
		t.Helper()
		var results []index.Result
		if err := json.Unmarshal(raw, &results); err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Page != 3 || results[0].Filename != "handbook.pdf" {
			t.Fatalf("wrong result: %s", raw)
		}
	}
	checkResult(runRelease(query...))
	paths, err := filepath.Glob(filepath.Join(cache, "pdfsearch-bundled", "*", "libonnxruntime.so.1.22.0"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("runtime cache: %v %v", paths, err)
	}
	info, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	before := info.ModTime()
	checkResult(runRelease(query...))
	info, err = os.Stat(paths[0])
	if err != nil || !info.ModTime().Equal(before) {
		t.Fatal("release replaced a valid cached library")
	}
	if err := os.WriteFile(paths[0], []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	checkResult(runRelease(query...))
	t.Log("Copied release indexed and searched offline, reused its cache, and repaired a corrupted runtime.")
}

type captureWriter []byte

func (w *captureWriter) Write(p []byte) (int, error) { *w = append(*w, p...); return len(p), nil }
