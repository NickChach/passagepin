package embedding

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestFullPageEmbeddingIntegration(t *testing.T) {
	dir := os.Getenv("PDFSEARCH_TEST_MODEL_DIR")
	library := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if dir == "" || library == "" {
		t.Skip("set PDFSEARCH_TEST_MODEL_DIR and ONNXRUNTIME_SHARED_LIBRARY_PATH for real inference")
	}
	tok, err := LoadTokenizer(filepath.Join(dir, "vocab.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]int64{{101, 2023, 2003, 2019, 2742, 6251, 102}}
	if got := tok.Windows("This is an example sentence"); !reflect.DeepEqual(got, want) {
		t.Fatalf("BERT vocabulary compatibility: got %v want %v", got, want)
	}
	m, err := Open(dir, library, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	prefix := strings.Repeat("Bread is made with flour and water. ", 40)
	if len(tok.Windows(prefix)) < 2 {
		t.Fatal("fixture does not exceed one model window")
	}
	a, err := m.Embed(context.Background(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Embed(context.Background(), prefix+strings.Repeat("Solar panels generate electricity from sunlight. ", 40))
	if err != nil {
		t.Fatal(err)
	}
	var cosine float64
	for i := range a {
		cosine += float64(a[i]) * float64(b[i])
	}
	if cosine > 0.99 {
		t.Fatalf("text beyond the first window had too little effect: cosine=%f", cosine)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Embed(ctx, "cancel this query"); err == nil {
		t.Fatal("ignored cancellation")
	}
	t.Logf("Late-page subject changes the page vector: cosine %.4f", cosine)
}

func TestConcurrentEmbeddingIntegration(t *testing.T) {
	dir, library := os.Getenv("PDFSEARCH_TEST_MODEL_DIR"), os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if dir == "" || library == "" {
		t.Skip("set local model and runtime paths")
	}
	m, err := Open(dir, library, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	texts := []string{"Bread and water.", strings.Repeat("Solar panels generate electricity from sunlight. ", 45), "Wind turbines produce renewable energy.", strings.Repeat("Gardens need soil and water. ", 80)}
	want := make([][]float32, len(texts))
	for i, text := range texts {
		want[i], err = m.Embed(context.Background(), text)
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i, text := range texts {
		wg.Add(1)
		go func(i int, text string) {
			defer wg.Done()
			for repeat := 0; repeat < 3; repeat++ {
				got, err := m.Embed(context.Background(), text)
				if err != nil {
					t.Error(err)
					return
				}
				for j := range got {
					if math.Float32bits(got[j]) != math.Float32bits(want[i][j]) {
						t.Errorf("concurrent vector changed for text %d component %d", i, j)
						return
					}
				}
			}
		}(i, text)
	}
	wg.Wait()
}

func BenchmarkEmbeddingStages(b *testing.B) {
	dir, library := os.Getenv("PDFSEARCH_TEST_MODEL_DIR"), os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if dir == "" || library == "" {
		b.Skip("set local model and runtime paths")
	}
	text := strings.Repeat("Solar panels convert sunlight into electricity. Wind turbines generate renewable energy without burning fossil fuels. ", 12)
	for _, threads := range []int{1, 2, 4} {
		b.Run(fmt.Sprintf("threads=%d", threads), func(b *testing.B) {
			m, err := Open(dir, library, threads)
			if err != nil {
				b.Fatal(err)
			}
			defer m.Close()
			windows := m.tokenizer.Windows(text)
			b.Run("tokenization", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					m.tokenizer.Windows(text)
				}
			})
			b.Run("inference_and_pooling", func(b *testing.B) {
				sum := make([]float64, Dimensions)
				for i := 0; i < b.N; i++ {
					clear(sum)
					for _, ids := range windows {
						if err := m.run(ids, sum); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		})
	}
}
