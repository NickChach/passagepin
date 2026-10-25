package embedding

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
