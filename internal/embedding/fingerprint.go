package embedding

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func (a Assets) Fingerprint() (string, error) {
	if a.Directory != "" {
		return Fingerprint(a.Directory)
	}
	if len(a.Weights) == 0 || len(a.Vocabulary) == 0 {
		return "", fmt.Errorf("model assets are missing")
	}
	assets := map[string][]byte{"vocab.txt": a.Vocabulary, "model.onnx": a.Weights}
	return fingerprint(func(name string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(assets[name])), nil
	})
}

// Fingerprint binds an index to the exact vocabulary, weights and pooling policy.
func Fingerprint(dir string) (string, error) {
	return fingerprint(func(name string) (io.ReadCloser, error) {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("open model asset: %w (run pdfsearch download)", err)
		}
		return f, nil
	})
}

// Keep the prefix, NUL separators and asset order identical for external and
// embedded assets. Changing these bytes invalidates existing model fingerprints.
func fingerprint(open func(string) (io.ReadCloser, error)) (string, error) {
	h := sha256.New()
	io.WriteString(h, ModelID+"\x00"+Pipeline+"\x00")
	for _, name := range []string{"vocab.txt", "model.onnx"} {
		asset, err := open(name)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, asset)
		closeErr := asset.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
