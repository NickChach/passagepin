// Package bundle prepares native dependencies embedded by a bundled release.
package bundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

type File struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	Version          int      `json:"version"`
	OS               string   `json:"os"`
	Arch             string   `json:"arch"`
	Runtime          string   `json:"runtime"`
	Model            string   `json:"model"`
	ModelRevision    string   `json:"model_revision"`
	ModelSHA256      string   `json:"model_sha256"`
	VocabularySHA256 string   `json:"vocabulary_sha256"`
	MinimumGLIBC     string   `json:"minimum_glibc"`
	Library          string   `json:"library"`
	Preload          []string `json:"preload"`
	Files            []File   `json:"files"`
}

func manifest() (Manifest, []byte, error) {
	if !Available {
		return Manifest{}, nil, fmt.Errorf("this executable does not contain bundled assets")
	}
	raw, err := fs.ReadFile(payload, "assets/manifest.json")
	if err != nil {
		return Manifest{}, nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, nil, err
	}
	if m.Version != 1 || m.OS != runtime.GOOS || m.Arch != runtime.GOARCH {
		return m, nil, fmt.Errorf("bundle does not match this OS/architecture")
	}
	return m, raw, nil
}

// Prepare extracts verified native files only. Model weights and the vocabulary
// remain in the executable and are read directly from memory.
func Prepare(ctx context.Context) (string, error) {
	m, raw, err := manifest()
	if err != nil {
		return "", err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate bundle cache: %w", err)
	}
	path, err := install(ctx, payload, cache, m, raw)
	if err != nil {
		return "", fmt.Errorf("prepare bundled runtime: %w", err)
	}
	var dependencies []string
	for _, n := range m.Preload {
		dependencies = append(dependencies, filepath.Join(path, n))
	}
	if err := preload(dependencies); err != nil {
		return "", fmt.Errorf("%w (this release requires glibc %s or newer)", err, m.MinimumGLIBC)
	}
	return filepath.Join(path, m.Library), nil
}

func Licenses() ([]byte, error) {
	if !Available {
		return nil, fmt.Errorf("license text is embedded in bundled releases; see THIRD_PARTY_NOTICES.md and dependency licenses for a source build")
	}
	return fs.ReadFile(payload, "assets/licenses.txt")
}

func Info() (Manifest, error) { m, _, err := manifest(); return m, err }

func privateDir(path string) error {
	err := os.Mkdir(path, 0700)
	if err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("cache path must be a private directory (mode 0700): %s", path)
	}
	return nil
}

func install(ctx context.Context, source fs.FS, cache string, m Manifest, raw []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(cache, 0755); err != nil {
		return "", err
	}
	base := filepath.Join(cache, "pdfsearch-bundled")
	if err := privateDir(base); err != nil {
		return "", err
	}
	id := sha256.Sum256(raw)
	dir := filepath.Join(base, fmt.Sprintf("%s-%s-%x", m.OS, m.Arch, id[:12]))
	if err := privateDir(dir); err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, asset := range m.Files {
		if asset.Name == "." || filepath.Base(asset.Name) != asset.Name || asset.Size < 1 || len(asset.SHA256) != 64 || seen[asset.Name] {
			return "", fmt.Errorf("invalid bundled asset %q", asset.Name)
		}
		if _, err := hex.DecodeString(asset.SHA256); err != nil {
			return "", err
		}
		seen[asset.Name] = true
		if err := installFile(ctx, source, dir, asset); err != nil {
			return "", err
		}
	}
	if !seen[m.Library] {
		return "", fmt.Errorf("native library missing from bundle manifest")
	}
	for _, n := range m.Preload {
		if !seen[n] {
			return "", fmt.Errorf("preloaded dependency missing from manifest: %s", n)
		}
	}
	return dir, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func installFile(ctx context.Context, source fs.FS, dir string, asset File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := filepath.Join(dir, asset.Name)
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular cache file: %s", path)
		}
		if info.Size() == asset.Size && info.Mode().Perm()&0077 == 0 {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			h := sha256.New()
			_, copyErr := io.Copy(h, contextReader{ctx, f})
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if hex.EncodeToString(h.Sum(nil)) == asset.SHA256 {
				return nil
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	in, err := source.Open("assets/runtime/" + asset.Name)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".extract-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(contextReader{ctx, in}, asset.Size+1))
	if err != nil {
		return err
	}
	if n != asset.Size || hex.EncodeToString(h.Sum(nil)) != asset.SHA256 {
		return fmt.Errorf("embedded asset checksum mismatch: %s", asset.Name)
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(out.Name(), path)
}
