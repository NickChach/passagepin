package index

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/darkearl/semantic-search/internal/embedding"
)

// Version 2 requires rebuilding vectors produced by the previous PDF extractor.
const Version = 2

type Page struct {
	Number int       `json:"number"`
	Vector []float32 `json:"vector"`
}

type Document struct {
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	PageCount int    `json:"page_count"`
	Pages     []Page `json:"pages"`
}

type Index struct {
	Version     int        `json:"version"`
	Root        string     `json:"root"`
	Model       string     `json:"model"`
	Fingerprint string     `json:"fingerprint"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Documents   []Document `json:"documents"`
}

type Extractor interface {
	PageCount(context.Context, string) (int, error)
	PageText(context.Context, string, int) (string, error)
}

type Embedder interface {
	Embed(context.Context, string) ([]float32, error)
}

type Stats struct {
	Indexed    int `json:"indexed"`
	Reused     int `json:"reused"`
	Pages      int `json:"pages"`
	EmptyPages int `json:"empty_pages"`
}

type Progress struct {
	Phase    string `json:"phase"`
	Filename string `json:"filename,omitempty"`
	Page     int    `json:"page,omitempty"`
	Pages    int    `json:"pages,omitempty"`
}

func Load(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var idx Index
	dec := json.NewDecoder(f)
	if err := dec.Decode(&idx); err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing index data")
	}
	if err := idx.Validate(); err != nil {
		return nil, err
	}
	return &idx, nil
}

func (idx *Index) Validate() error {
	if idx.Version != Version || idx.Model != embedding.ModelID || idx.Fingerprint == "" || !filepath.IsAbs(idx.Root) {
		return fmt.Errorf("incompatible index; rebuild with pdfsearch index --rebuild")
	}
	seen := make(map[string]bool)
	for _, doc := range idx.Documents {
		if doc.Filename == "." || !filepath.IsLocal(doc.Filename) || seen[doc.Filename] || len(doc.SHA256) != 64 || doc.PageCount < 1 {
			return fmt.Errorf("invalid index document %q", doc.Filename)
		}
		seen[doc.Filename] = true
		previous := 0
		for _, page := range doc.Pages {
			if page.Number <= previous || page.Number > doc.PageCount {
				return fmt.Errorf("invalid page number for %q", doc.Filename)
			}
			previous = page.Number
			if err := validateVector(page.Vector); err != nil {
				return fmt.Errorf("%s page %d: %w", doc.Filename, page.Number, err)
			}
		}
	}
	return nil
}

func validateVector(vector []float32) error {
	if len(vector) != embedding.Dimensions {
		return fmt.Errorf("embedding must have %d dimensions", embedding.Dimensions)
	}
	var norm float64
	for _, v := range vector {
		norm += float64(v) * float64(v)
	}
	if math.IsNaN(norm) || math.Abs(norm-1) > 0.001 {
		return fmt.Errorf("embedding must be finite and normalized")
	}
	return nil
}

// Save replaces the index only after the entire new index is flushed to disk.
func (idx *Index) Save(path string) error {
	if err := idx.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".index-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := json.NewEncoder(f).Encode(idx); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func fileHash(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buffer)
		if n > 0 {
			h.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// Build keeps page text in memory only while embedding that page. It reuses
// unchanged PDFs, and naturally removes deleted documents from the new snapshot.
func Build(ctx context.Context, root, fingerprint string, recursive bool, old *Index, extractor Extractor, embedder Embedder, progress io.Writer) (*Index, Stats, error) {
	return BuildWithProgress(ctx, root, fingerprint, recursive, old, extractor, embedder, progress, nil)
}

// BuildWithProgress supplies structured events to a GUI without parsing logs.
func BuildWithProgress(ctx context.Context, root, fingerprint string, recursive bool, old *Index, extractor Extractor, embedder Embedder, progress io.Writer, emit func(Progress)) (*Index, Stats, error) {
	notify := func(p Progress) {
		if emit != nil {
			emit(p)
		}
	}
	notify(Progress{Phase: "discovering"})
	var stats Stats
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, stats, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, stats, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, stats, err
	}
	if !info.IsDir() {
		return nil, stats, fmt.Errorf("PDF root must be a directory")
	}
	cache := make(map[string]Document)
	if old != nil {
		if old.Root != root || old.Fingerprint != fingerprint {
			return nil, stats, fmt.Errorf("index belongs to another directory or model; use --rebuild")
		}
		for _, doc := range old.Documents {
			cache[doc.Filename] = doc
		}
	}
	idx := &Index{Version: Version, Root: root, Model: embedding.ModelID, Fingerprint: fingerprint, UpdatedAt: time.Now().UTC(), Documents: []Document{}}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && !recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".pdf") {
			return nil
		}
		// Do not follow file or directory symlinks outside the selected tree.
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		digest, err := fileHash(ctx, path)
		if err != nil {
			return err
		}
		if doc, ok := cache[rel]; ok && doc.SHA256 == digest {
			notify(Progress{Phase: "reused", Filename: rel, Pages: doc.PageCount})
			idx.Documents = append(idx.Documents, doc)
			stats.Reused++
			stats.Pages += len(doc.Pages)
			stats.EmptyPages += doc.PageCount - len(doc.Pages)
			fmt.Fprintf(progress, "Unchanged: %s\n", rel)
			return nil
		}
		count, err := extractor.PageCount(ctx, path)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		doc := Document{Filename: rel, SHA256: digest, PageCount: count, Pages: []Page{}}
		for page := 1; page <= count; page++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			fmt.Fprintf(progress, "Indexing: %s page %d/%d\n", rel, page, count)
			notify(Progress{Phase: "embedding", Filename: rel, Page: page, Pages: count})
			text, err := extractor.PageText(ctx, path, page)
			if err != nil {
				return fmt.Errorf("%s page %d: %w", rel, page, err)
			}
			if strings.TrimSpace(text) == "" {
				stats.EmptyPages++
				continue
			}
			vector, err := embedder.Embed(ctx, text)
			if err != nil {
				return fmt.Errorf("%s page %d: %w", rel, page, err)
			}
			if err := validateVector(vector); err != nil {
				return err
			}
			doc.Pages = append(doc.Pages, Page{Number: page, Vector: vector})
			stats.Pages++
		}
		// A changed source must never be committed under a stale content digest.
		after, err := fileHash(ctx, path)
		if err != nil {
			return err
		}
		if after != digest {
			return fmt.Errorf("%s changed during indexing; rerun index", rel)
		}
		idx.Documents = append(idx.Documents, doc)
		stats.Indexed++
		return nil
	})
	if err != nil {
		return nil, stats, err
	}
	return idx, stats, nil
}

type Result struct {
	Filename string  `json:"filename"`
	Page     int     `json:"page"`
	Score    float64 `json:"score"`
}

func (idx *Index) Search(query []float32, top int, minScore float64) ([]Result, error) {
	return idx.SearchContext(context.Background(), query, top, minScore)
}

func (idx *Index) SearchContext(ctx context.Context, query []float32, top int, minScore float64) ([]Result, error) {
	if top < 1 || math.IsNaN(minScore) || minScore < -1 || minScore > 1 {
		return nil, fmt.Errorf("top must be positive and min-score must be between -1 and 1")
	}
	if err := validateVector(query); err != nil {
		return nil, err
	}
	results := []Result{}
	for _, doc := range idx.Documents {
		for _, page := range doc.Pages {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var score float64
			for i, value := range page.Vector {
				score += float64(value) * float64(query[i])
			}
			score = max(-1, min(1, score))
			if score >= minScore {
				results = append(results, Result{Filename: doc.Filename, Page: page.Number, Score: score})
			}
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Filename != results[j].Filename {
			return results[i].Filename < results[j].Filename
		}
		return results[i].Page < results[j].Page
	})
	return results[:min(top, len(results))], nil
}
