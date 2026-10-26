package index

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkearl/semantic-search/internal/embedding"
	"github.com/darkearl/semantic-search/internal/pdf"
	"github.com/darkearl/semantic-search/internal/testpdf"
)

// Opt-in, offline benchmark. An explicit PDFSEARCH_BENCH_DIR keeps the generated
// corpus available for before/after CLI comparisons; otherwise use a temp dir.
func BenchmarkIndexReal(b *testing.B) {
	modelDir, library := os.Getenv("PDFSEARCH_TEST_MODEL_DIR"), os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if modelDir == "" || library == "" {
		b.Skip("set local model and runtime paths")
	}
	root := os.Getenv("PDFSEARCH_BENCH_DIR")
	if root == "" {
		root = b.TempDir()
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		b.Fatal(err)
	}
	text := strings.Repeat("Solar panels convert sunlight into electricity. Wind turbines generate renewable energy without burning fossil fuels. ", 12)
	for i := 0; i < 8; i++ {
		if err := testpdf.Write(filepath.Join(root, fmt.Sprintf("book-%02d.pdf", i)), []string{text, "", text + " Clean power.", text, text, text}); err != nil {
			b.Fatal(err)
		}
	}
	for _, config := range []struct{ readers, embedders, threads int }{{1, 1, 2}, {2, 1, 2}, {2, 2, 1}, {2, 2, 2}, {2, 4, 1}} {
		name := fmt.Sprintf("readers=%d/embedders=%d/threads=%d", config.readers, config.embedders, config.threads)
		b.Run(name, func(b *testing.B) {
			model, err := embedding.Open(modelDir, library, config.threads)
			if err != nil {
				b.Fatal(err)
			}
			defer model.Close()
			if _, err := model.Embed(context.Background(), text); err != nil {
				b.Fatal(err)
			}
			savePath := filepath.Join(b.TempDir(), "index.json")
			var totals Timings
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				idx, stats, err := BuildConcurrent(context.Background(), root, "benchmark", true, nil, func() (Extractor, error) { return pdf.New() }, model, io.Discard, nil, Options{config.readers, config.embedders})
				if err != nil {
					b.Fatal(err)
				}
				totals.Hashing += stats.Timings.Hashing
				totals.Extraction += stats.Timings.Extraction
				totals.Embedding += stats.Timings.Embedding
				saving := time.Now()
				if err := idx.Save(savePath); err != nil {
					b.Fatal(err)
				}
				totals.Saving += time.Since(saving)
			}
			b.StopTimer()
			b.ReportMetric(float64(totals.Hashing)/float64(b.N)/1e6, "hash-ms/op")
			b.ReportMetric(float64(totals.Extraction)/float64(b.N)/1e6, "extract-ms/op")
			b.ReportMetric(float64(totals.Embedding)/float64(b.N)/1e6, "embed-ms/op")
			b.ReportMetric(float64(totals.Saving)/float64(b.N)/1e6, "save-ms/op")
		})
	}
}
