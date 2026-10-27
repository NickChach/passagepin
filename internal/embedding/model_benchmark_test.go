package embedding

import (
	"context"
	"os"
	"strings"
	"testing"
)

func embeddingFixtures() []struct{ name, text string } {
	return []struct{ name, text string }{
		{"short", "This is an example sentence"},
		{"multi_window", strings.Repeat("Solar panels convert sunlight into electricity. Wind turbines generate renewable energy without burning fossil fuels. ", 30)},
	}
}

// BenchmarkEmbed measures the complete production path, including tokenization,
// inference, pooling and normalization. Model loading and warmup are excluded.
func BenchmarkEmbed(b *testing.B) {
	dir, library := os.Getenv("PDFSEARCH_TEST_MODEL_DIR"), os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if dir == "" || library == "" {
		b.Skip("set local model and runtime paths")
	}
	m, err := Open(dir, library, 1)
	if err != nil {
		b.Fatal(err)
	}
	defer m.Close()
	for _, fixture := range embeddingFixtures() {
		b.Run(fixture.name, func(b *testing.B) {
			windows := m.tokenizer.Windows(fixture.text)
			var tokens int
			for _, ids := range windows {
				tokens += len(ids)
			}
			for i := 0; i < 5; i++ {
				if _, err := m.Embed(context.Background(), fixture.text); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := m.Embed(context.Background(), fixture.text); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(windows)), "windows/op")
			b.ReportMetric(float64(tokens), "tokens/op")
		})
	}
}
