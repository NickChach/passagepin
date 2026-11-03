package embedding

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedAndExternalFingerprintCompatibility(t *testing.T) {
	dir := t.TempDir()
	weights, vocab := []byte("test weights"), []byte("test vocabulary")
	for name, data := range map[string][]byte{"model.onnx": weights, "vocab.txt": vocab} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := (Assets{Directory: dir}).Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	b, err := (Assets{Weights: weights, Vocabulary: vocab}).Fingerprint()
	if err != nil || a != b {
		t.Fatalf("embedded/external fingerprint: %s vs %s, %v", a, b, err)
	}
	// Pin the existing serialized contract, not just agreement between two paths.
	const want = "bb0278ff9cbfe9eda8a943908f1c790cefa2f6161b3aa4a12ed6cc6f1e169455"
	if a != want {
		t.Fatalf("model fingerprint changed: got %s want %s", a, want)
	}
}
