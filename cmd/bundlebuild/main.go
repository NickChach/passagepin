// bundlebuild stages local, pinned model/runtime assets and builds one standalone
// Linux amd64 release. It uses no Python and performs no asset downloads.
package main

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/darkearl/semantic-search/internal/bundle"
	"github.com/darkearl/semantic-search/internal/embedding"
)

func main() {
	model := flag.String("model-dir", ".models", "local pinned MiniLM model and vocabulary")
	native := flag.String("runtime-dir", ".runtime/onnxruntime-linux-x64-1.22.0", "extracted official ONNX Runtime 1.22.0 release")
	output := flag.String("out", "dist/pdfsearch-linux-amd64", "bundled executable destination")
	flag.Parse()
	if err := build(context.Background(), *model, *native, *output); err != nil {
		fmt.Fprintln(os.Stderr, "Bundle build:", err)
		os.Exit(1)
	}
}
func goCommand(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func minimumGLIBC(path string) (string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if f.Machine != elf.EM_X86_64 || f.Class != elf.ELFCLASS64 {
		return "", fmt.Errorf("expected a Linux x86-64 ELF file: %s", path)
	}
	symbols, err := f.ImportedSymbols()
	if err != nil {
		return "", err
	}
	minimum := "0.0"
	for _, s := range symbols {
		if version, ok := strings.CutPrefix(s.Version, "GLIBC_"); ok && newer(version, minimum) {
			minimum = version
		}
	}
	return minimum, nil
}
func newer(a, b string) bool {
	av, bv := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(av), len(bv)); i++ {
		x, y := 0, 0
		if i < len(av) {
			x, _ = strconv.Atoi(av[i])
		}
		if i < len(bv) {
			y, _ = strconv.Atoi(bv[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
func systemLibrary(name string) (string, error) {
	for _, dir := range []string{"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu", "/lib64", "/usr/lib64"} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", fmt.Errorf("cannot locate native dependency %s on this build host", name)
}
func build(ctx context.Context, model, native, output string) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("bundled builds currently support Linux amd64")
	}
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	version, err := os.ReadFile(filepath.Join(native, "VERSION_NUMBER"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(version)) != "1.22.0" {
		return fmt.Errorf("native runtime must be version 1.22.0")
	}
	// Probe a regular build so the declared baseline includes the executable's
	// cgo/glibc requirements as well as those of all embedded libraries.
	probe, err := os.CreateTemp("", "pdfsearch-build-probe-*")
	if err != nil {
		return err
	}
	probe.Close()
	defer os.Remove(probe.Name())
	if err := goCommand(ctx, "build", "-buildvcs=false", "-o", probe.Name(), "./cmd/pdfsearch"); err != nil {
		return err
	}
	baseline, err := minimumGLIBC(probe.Name())
	if err != nil {
		return err
	}
	weights, err := os.ReadFile(filepath.Join(model, "model.onnx"))
	if err != nil {
		return fmt.Errorf("model missing; run pdfsearch download --model-dir %s: %w", model, err)
	}
	vocab, err := os.ReadFile(filepath.Join(model, "vocab.txt"))
	if err != nil {
		return err
	}
	if len(weights) == 0 {
		return fmt.Errorf("model weights are empty")
	}
	if _, err := embedding.LoadTokenizer(filepath.Join(model, "vocab.txt")); err != nil {
		return err
	}
	root := filepath.Join("internal", "bundle", "assets")
	if err := os.MkdirAll(filepath.Join(root, "runtime"), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "model.onnx"), weights, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "vocab.txt"), vocab, 0644); err != nil {
		return err
	}
	library := "libonnxruntime.so.1.22.0"
	paths := map[string]string{library: filepath.Join(native, "lib", library)}
	for _, name := range []string{"libgcc_s.so.1", "libstdc++.so.6"} {
		path, err := systemLibrary(name)
		if err != nil {
			return err
		}
		paths[name] = path
	}
	manifest := bundle.Manifest{Version: 1, OS: "linux", Arch: "amd64", Runtime: "1.22.0", Model: embedding.ModelID, ModelRevision: embedding.Revision, ModelSHA256: digest(weights), VocabularySHA256: digest(vocab), Library: library, Preload: []string{"libgcc_s.so.1", "libstdc++.so.6"}}
	var names []string
	for name := range paths {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		path := paths[name]
		required, err := minimumGLIBC(path)
		if err != nil {
			return err
		}
		if newer(required, baseline) {
			baseline = required
		}
		f, err := elf.Open(path)
		if err != nil {
			return err
		}
		needed, err := f.ImportedLibraries()
		f.Close()
		if err != nil {
			return err
		}
		for _, dep := range needed {
			if _, bundled := paths[dep]; bundled {
				continue
			}
			if !slices.Contains([]string{"libc.so.6", "libm.so.6", "libdl.so.2", "librt.so.1", "libpthread.so.0", "ld-linux-x86-64.so.2"}, dep) {
				return fmt.Errorf("unhandled native dependency %s required by %s", dep, path)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, bundle.File{Name: name, SHA256: digest(data), Size: int64(len(data))})
		if err := os.WriteFile(filepath.Join(root, "runtime", name), data, 0644); err != nil {
			return err
		}
	}
	manifest.MinimumGLIBC = baseline
	notices, err := licenses(ctx, native)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "licenses.txt"), notices, 0644); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	// Build to a temporary destination so a failed rebuild preserves the release.
	target, err := os.CreateTemp(filepath.Dir(output), ".pdfsearch-release-*")
	if err != nil {
		return err
	}
	target.Close()
	defer os.Remove(target.Name())
	if err := goCommand(ctx, "build", "-buildvcs=false", "-trimpath", "-tags=bundled", "-ldflags=-s -w", "-o", target.Name(), "./cmd/pdfsearch"); err != nil {
		return err
	}
	if err := os.Rename(target.Name(), output); err != nil {
		return err
	}
	info, err := os.Stat(output)
	if err != nil {
		return err
	}
	fmt.Printf("Built %s (%.1f MiB), Linux amd64, glibc >= %s. No asset downloads are needed at runtime.\n", output, float64(info.Size())/(1<<20), baseline)
	return nil
}
func licenses(ctx context.Context, native string) ([]byte, error) {
	var out strings.Builder
	fmt.Fprintf(&out, "pdfsearch bundled release\nModel: %s\nRevision: %s\nModel license: Apache-2.0\n\n", embedding.ModelID, embedding.Revision)
	files := []struct{ label, path string }{
		{"PDF glyph data", "THIRD_PARTY_NOTICES.md"},
		{"Go standard library", filepath.Join(runtime.GOROOT(), "LICENSE")},
		{"ONNX Runtime", filepath.Join(native, "LICENSE")},
		{"ONNX Runtime third-party notices", filepath.Join(native, "ThirdPartyNotices.txt")},
		{"MiniLM model / Apache License 2.0", "/usr/share/common-licenses/Apache-2.0"},
		{"GNU C++ runtime and GCC runtime exceptions", "/usr/share/doc/libstdc++6/copyright"},
		{"GCC support runtime", "/usr/share/doc/libgcc-s1/copyright"},
		{"GNU GPL version 3", "/usr/share/common-licenses/GPL-3"},
		{"GNU LGPL version 3", "/usr/share/common-licenses/LGPL-3"},
		{"GNU GPL version 2", "/usr/share/common-licenses/GPL-2"},
		{"GNU LGPL version 2.1", "/usr/share/common-licenses/LGPL-2.1"},
	}
	// Inspect modules actually linked into the CLI, excluding development-only
	// dependencies in the wider module graph.
	cmd := exec.CommandContext(ctx, "go", "list", "-buildvcs=false", "-deps", "-json", "./cmd/pdfsearch")
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list module licenses: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(output)))
	seen := map[string]bool{}
	for {
		var pkg struct {
			Module *struct {
				Path, Dir string
				Main      bool
			}
		}
		err := dec.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		mod := pkg.Module
		if mod == nil || mod.Main || seen[mod.Path] {
			continue
		}
		seen[mod.Path] = true
		files = append(files, struct{ label, path string }{mod.Path, filepath.Join(mod.Dir, "LICENSE")})
	}
	for _, file := range files {
		data, err := os.ReadFile(file.path)
		if err != nil {
			return nil, fmt.Errorf("read required license %s: %w", file.path, err)
		}
		fmt.Fprintf(&out, "\n===== %s =====\n\n%s\n", file.label, data)
	}
	return []byte(out.String()), nil
}
