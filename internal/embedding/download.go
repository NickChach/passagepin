package embedding

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Download uses an immutable Hugging Face revision and atomic file replacement.
// Existing assets are replaced so this command also repairs interrupted setups.
func Download(ctx context.Context, dir string, progress io.Writer) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Minute}
	for _, asset := range []struct{ remote, local string }{{"onnx/model.onnx", "model.onnx"}, {"vocab.txt", "vocab.txt"}} {
		fmt.Fprintf(progress, "Downloading %s at %s…\n", asset.remote, Revision)
		url := "https://huggingface.co/" + ModelID + "/resolve/" + Revision + "/" + asset.remote
		if err := downloadFile(ctx, client, url, filepath.Join(dir, asset.local)); err != nil {
			return err
		}
	}
	return nil
}

func downloadFile(ctx context.Context, client *http.Client, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", path, resp.Status)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, 512*1024*1024+1))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n == 0 || n > 512*1024*1024 {
		return fmt.Errorf("unexpected asset size: %d", n)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return fmt.Errorf("incomplete asset download")
	}
	return os.Rename(f.Name(), path)
}
