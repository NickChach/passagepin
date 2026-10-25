package bundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
)

func fixture(t *testing.T) (fstest.MapFS, Manifest, []byte) {
	t.Helper()
	data := []byte("fake native library bytes")
	sum := sha256.Sum256(data)
	m := Manifest{Version: 1, OS: "linux", Arch: "amd64", Library: "runtime.so", Files: []File{{Name: "runtime.so", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{"assets/runtime/runtime.so": &fstest.MapFile{Data: data}}, m, raw
}

func TestCacheInstallationReuseAndRepair(t *testing.T) {
	source, m, raw := fixture(t)
	cache := t.TempDir()
	dir, err := install(context.Background(), source, cache, m, raw)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, m.Library)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("native file mode: %v", info.Mode())
	}
	before := info.ModTime()
	if _, err := install(context.Background(), source, cache, m, raw); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil || !info.ModTime().Equal(before) {
		t.Fatal("valid cache was needlessly replaced")
	}
	if err := os.WriteFile(path, []byte("corrupt native library!!!"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := install(context.Background(), source, cache, m, raw); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "fake native library bytes" {
		t.Fatalf("cache repair: %q %v", got, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := install(context.Background(), source, cache, m, raw); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("did not restore private file permissions")
	}
}

func TestCacheConcurrentExtraction(t *testing.T) {
	source, m, raw := fixture(t)
	cache := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := install(context.Background(), source, cache, m, raw); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := install(context.Background(), source, cache, m, raw); err != nil {
		t.Fatal(err)
	}
}

func TestCacheRejectsSymlinksAndUntrustedPayload(t *testing.T) {
	source, m, raw := fixture(t)
	cache := t.TempDir()
	dir, err := install(context.Background(), source, cache, m, raw)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, m.Library)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skip(err)
	}
	if _, err := install(context.Background(), source, cache, m, raw); err == nil {
		t.Fatal("accepted a native library symlink")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "untouched" {
		t.Fatal("modified a symlink target")
	}
	bad := fstest.MapFS{"assets/runtime/runtime.so": &fstest.MapFile{Data: []byte("wrong bytes")}}
	if _, err := install(context.Background(), bad, t.TempDir(), m, raw); err == nil {
		t.Fatal("installed unverified embedded data")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := install(ctx, source, t.TempDir(), m, raw); err == nil {
		t.Fatal("ignored cancellation")
	}
}
