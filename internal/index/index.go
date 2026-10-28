package index

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/darkearl/semantic-search/internal/embedding"
)

// Version 7 rebuilds vectors after transformed-text spacing fixes and discards
// whole documents when cross-reference errors persist after recovery retries.
const Version = 7

type Page struct {
	Number int       `json:"number"`
	Vector []float32 `json:"vector"`
}

type Document struct {
	Filename  string        `json:"filename"`
	SHA256    string        `json:"sha256"`
	PageCount int           `json:"page_count"`
	Pages     []Page        `json:"pages"`
	Recovery  string        `json:"recovery,omitempty"`
	Failures  []PageFailure `json:"failures,omitempty"`
}

type PageFailure struct {
	Page    int    `json:"page"`
	Reason  string `json:"reason"`
	Feature string `json:"feature,omitempty"`
}

type Index struct {
	Version     int         `json:"version"`
	Root        string      `json:"root"`
	Model       string      `json:"model"`
	Fingerprint string      `json:"fingerprint"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Documents   []Document  `json:"documents"`
	Issues      []FileIssue `json:"issues,omitempty"`
}

type FileIssue struct {
	Filename     string        `json:"filename"`
	Kind         string        `json:"kind"` // recovered, partial, or skipped
	Reason       string        `json:"reason"`
	Page         int           `json:"page,omitempty"`
	Feature      string        `json:"feature,omitempty"`
	IndexedPages int           `json:"indexed_pages,omitempty"`
	Failures     []PageFailure `json:"failures,omitempty"`
}

type Extractor interface {
	PageCount(context.Context, string) (int, error)
	PageText(context.Context, string, int) (string, error)
}

type Embedder interface {
	Embed(context.Context, string) ([]float32, error)
}

type Stats struct {
	Indexed     int         `json:"indexed"`
	Reused      int         `json:"reused"`
	Pages       int         `json:"pages"`
	EmptyPages  int         `json:"empty_pages"`
	Timings     Timings     `json:"timings"`
	Recovered   int         `json:"recovered"`
	Skipped     int         `json:"skipped"`
	Partial     int         `json:"partial"`
	FailedPages int         `json:"failed_pages"`
	Issues      []FileIssue `json:"issues,omitempty"`
}

// Timings are cumulative worker time, except Total (build wall time) and Saving.
// Embedding includes tokenization and inference. Durations encode as nanoseconds.
type Timings struct {
	Hashing    time.Duration `json:"hashing_ns"`
	Extraction time.Duration `json:"extraction_ns"`
	Embedding  time.Duration `json:"embedding_ns"`
	Total      time.Duration `json:"total_ns"`
	Saving     time.Duration `json:"saving_ns"`
}

type Progress struct {
	Phase     string `json:"phase"`
	Filename  string `json:"filename,omitempty"`
	Page      int    `json:"page,omitempty"`
	Pages     int    `json:"pages,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Recovered bool   `json:"recovered,omitempty"`
	Partial   bool   `json:"partial,omitempty"`
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
	partial := make(map[string]FileIssue)
	reports := make(map[[2]string]bool)
	for _, issue := range idx.Issues {
		if !filepath.IsLocal(issue.Filename) || issue.Filename == "." || (issue.Kind != "skipped" && issue.Kind != "recovered" && issue.Kind != "partial") || issue.Reason == "" || issue.Page < 0 || issue.IndexedPages < 0 {
			return fmt.Errorf("invalid index file issue")
		}
		if issue.Kind == "partial" && (issue.IndexedPages < 1 || len(issue.Failures) == 0) {
			return fmt.Errorf("invalid partial index file issue")
		}
		if err := validateFailures(issue.Failures, 0); err != nil {
			return err
		}
		key := [2]string{issue.Filename, issue.Kind}
		if reports[key] {
			return fmt.Errorf("duplicate index file issue")
		}
		reports[key] = true
		if issue.Kind == "partial" {
			partial[issue.Filename] = issue
		}
	}
	for _, doc := range idx.Documents {
		if doc.Filename == "." || !filepath.IsLocal(doc.Filename) || seen[doc.Filename] || len(doc.SHA256) != 64 || doc.PageCount < 1 {
			return fmt.Errorf("invalid index document %q", doc.Filename)
		}
		seen[doc.Filename] = true
		issue, reported := partial[doc.Filename]
		if reports[[2]string{doc.Filename, "skipped"}] || (len(doc.Failures) > 0) != reported || reported && (issue.IndexedPages != len(doc.Pages) || !slices.Equal(issue.Failures, doc.Failures)) {
			return fmt.Errorf("inconsistent page failure report for %q", doc.Filename)
		}
		delete(partial, doc.Filename)
		if err := validateFailures(doc.Failures, doc.PageCount); err != nil {
			return err
		}
		failed := make(map[int]bool, len(doc.Failures))
		for _, failure := range doc.Failures {
			failed[failure.Page] = true
		}
		previous := 0
		for _, page := range doc.Pages {
			if page.Number <= previous || page.Number > doc.PageCount || failed[page.Number] {
				return fmt.Errorf("invalid page number for %q", doc.Filename)
			}
			previous = page.Number
			if err := validateVector(page.Vector); err != nil {
				return fmt.Errorf("%s page %d: %w", doc.Filename, page.Number, err)
			}
		}
	}
	if len(partial) > 0 {
		return fmt.Errorf("partial report has no indexed document")
	}
	return nil
}

func validateFailures(failures []PageFailure, count int) error {
	previous := 0
	for _, failure := range failures {
		if failure.Page <= previous || (count > 0 && failure.Page > count) || failure.Reason == "" {
			return fmt.Errorf("invalid index page failure")
		}
		previous = failure.Page
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

// Build overlaps extraction and embedding with one worker for each stage.
// It reuses unchanged PDFs and removes deleted documents from the new snapshot.
func Build(ctx context.Context, root, fingerprint string, recursive bool, old *Index, extractor Extractor, embedder Embedder, progress io.Writer) (*Index, Stats, error) {
	return BuildWithProgress(ctx, root, fingerprint, recursive, old, extractor, embedder, progress, nil)
}

// BuildWithProgress supplies structured events to a GUI without parsing logs.
func BuildWithProgress(ctx context.Context, root, fingerprint string, recursive bool, old *Index, extractor Extractor, embedder Embedder, progress io.Writer, emit func(Progress)) (*Index, Stats, error) {
	return buildPipeline(ctx, root, fingerprint, recursive, old, []Extractor{extractor}, embedder, progress, emit, 1)
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
