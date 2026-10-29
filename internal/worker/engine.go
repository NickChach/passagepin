// Package worker serves a persistent, serial search engine over JSON lines.
package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darkearl/semantic-search/internal/index"
	"github.com/darkearl/semantic-search/internal/pdf"
)

type Model interface {
	Embed(context.Context, string) ([]float32, error)
}

// Engine operations must be serialized by the caller. Serve owns that guard.
type Engine struct {
	model                Model
	fingerprint          string
	directory, indexPath string
	idx                  *index.Index
	issue                string
	indexOptions         index.Options
}

type Library struct {
	Directory   string            `json:"directory"`
	Indexed     bool              `json:"indexed"`
	Documents   int               `json:"documents"`
	Pages       int               `json:"pages"`
	EmptyPages  int               `json:"empty_pages"`
	FailedPages int               `json:"failed_pages"`
	UpdatedAt   *time.Time        `json:"updated_at"`
	Issue       string            `json:"issue,omitempty"`
	Issues      []index.FileIssue `json:"issues,omitempty"`
}

func New(model Model, fingerprint string) *Engine {
	return NewWithOptions(model, fingerprint, index.DefaultOptions())
}

func NewWithOptions(model Model, fingerprint string, options index.Options) *Engine {
	return &Engine{model: model, fingerprint: fingerprint, indexOptions: options}
}

func (e *Engine) summary() Library {
	l := Library{Directory: e.directory, Indexed: e.idx != nil, Issue: e.issue}
	if e.idx != nil {
		l.Issues = e.idx.Issues
		counts := e.idx.Counts()
		l.Documents, l.Pages = counts.Documents, counts.Pages
		l.EmptyPages, l.FailedPages = counts.EmptyPages, counts.FailedPages
		l.UpdatedAt = &e.idx.UpdatedAt
	}
	return l
}

type IndexResult struct {
	Library Library     `json:"library"`
	Stats   index.Stats `json:"stats"`
}
type SearchResponse struct {
	Results []index.Result `json:"results"`
	Query   string         `json:"query"`
}
type PageText struct {
	Filename string `json:"filename"`
	Page     int    `json:"page"`
	Text     string `json:"text"`
	Path     string `json:"path"`
}
type PDFLocation struct {
	Path string `json:"path"`
}

// ResolvePDF authorizes a file without decoding its contents. Paths are returned
// only to the desktop main process; the renderer never receives them.
func (e *Engine) ResolvePDF(ctx context.Context, p PageParams) (PDFLocation, error) {
	if err := ctx.Err(); err != nil {
		return PDFLocation{}, err
	}
	path, err := e.pagePath(p.Filename, p.Page)
	if err != nil {
		return PDFLocation{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return PDFLocation{}, err
	}
	if !info.Mode().IsRegular() {
		return PDFLocation{}, fmt.Errorf("PDF is not a regular file")
	}
	return PDFLocation{Path: path}, nil
}

type OpenParams struct {
	Directory string `json:"directory"`
	IndexPath string `json:"index_path"`
}

func (e *Engine) Open(ctx context.Context, p OpenParams) (Library, error) {
	if err := ctx.Err(); err != nil {
		return Library{}, err
	}
	if p.Directory == "" || p.IndexPath == "" {
		return Library{}, fmt.Errorf("directory and index_path are required")
	}
	root, err := filepath.Abs(p.Directory)
	if err != nil {
		return Library{}, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Library{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return Library{}, err
	}
	if !info.IsDir() {
		return Library{}, fmt.Errorf("select a directory of PDFs")
	}
	e.directory, e.indexPath, e.idx, e.issue = root, p.IndexPath, nil, ""
	idx, err := index.Load(p.IndexPath)
	if errors.Is(err, os.ErrNotExist) {
		return e.summary(), nil
	}
	if err != nil {
		e.issue = err.Error()
		return e.summary(), nil
	}
	if idx.Root != root || idx.Fingerprint != e.fingerprint {
		e.issue = "The saved index uses another directory or model. Rebuild this index."
		return e.summary(), nil
	}
	e.idx = idx
	return e.summary(), nil
}

type IndexParams struct {
	Rebuild bool `json:"rebuild"`
}

func (e *Engine) BuildIndex(ctx context.Context, p IndexParams, emit func(index.Progress)) (IndexResult, error) {
	if err := ctx.Err(); err != nil {
		return IndexResult{}, err
	}
	if e.directory == "" {
		return IndexResult{}, fmt.Errorf("choose a folder first")
	}

	if e.issue != "" && !p.Rebuild {
		return IndexResult{}, fmt.Errorf("rebuild the incompatible index")
	}
	old := e.idx
	if p.Rebuild {
		old = nil
	}
	idx, stats, err := index.BuildConcurrent(ctx, e.directory, e.fingerprint, true, old, func() (index.Extractor, error) { return pdf.New() }, e.model, io.Discard, emit, e.indexOptions)
	if err != nil {
		return IndexResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return IndexResult{}, err
	}
	saving := time.Now()
	if err := idx.Save(e.indexPath); err != nil {
		return IndexResult{}, err
	}
	stats.Timings.Saving = time.Since(saving)
	e.idx, e.issue = idx, ""
	return IndexResult{e.summary(), stats}, nil
}

type SearchParams struct {
	Query    string  `json:"query"`
	Top      int     `json:"top"`
	MinScore float64 `json:"min_score"`
}

func (e *Engine) Search(ctx context.Context, p SearchParams) (SearchResponse, error) {
	if err := ctx.Err(); err != nil {
		return SearchResponse{}, err
	}
	if e.idx == nil {
		return SearchResponse{}, fmt.Errorf("index this folder before searching")
	}

	p.Query = strings.TrimSpace(p.Query)
	if p.Query == "" || len(p.Query) > 16000 {
		return SearchResponse{}, fmt.Errorf("enter a subject of up to 16,000 bytes")
	}
	if p.Top == 0 {
		p.Top = 20
	}
	if p.Top < 1 || p.Top > 500 {
		return SearchResponse{}, fmt.Errorf("top must be between 1 and 500")
	}
	vector, err := e.model.Embed(ctx, p.Query)
	if err != nil {
		return SearchResponse{}, err
	}
	results, err := e.idx.SearchContext(ctx, vector, p.Top, p.MinScore)
	if err != nil {
		return SearchResponse{}, err
	}
	return SearchResponse{results, p.Query}, nil
}

type PageParams struct {
	Filename string `json:"filename"`
	Page     int    `json:"page"`
}

func (e *Engine) Page(ctx context.Context, p PageParams) (PageText, error) {
	if err := ctx.Err(); err != nil {
		return PageText{}, err
	}
	path, err := e.pagePath(p.Filename, p.Page)
	if err != nil {
		return PageText{}, err
	}
	extractor, _ := pdf.New()
	defer extractor.Close()
	text, err := extractor.PageText(ctx, path, p.Page)
	if err != nil {
		return PageText{}, err
	}
	// Keep IPC previews bounded; indexing still embeds the entire page.
	if len(text) > 200000 {
		text = text[:200000] + "\n\n[Preview shortened. Open the PDF to read the full page.]"
	}
	return PageText{p.Filename, p.Page, text, path}, nil
}

func (e *Engine) pagePath(filename string, page int) (string, error) {
	if e.idx == nil || !filepath.IsLocal(filename) {
		return "", fmt.Errorf("page is not in the active library")
	}
	found := false
	for _, doc := range e.idx.Documents {
		if doc.Filename == filename && page >= 1 && page <= doc.PageCount {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("page is not in the active library")
	}
	path, err := filepath.EvalSymlinks(filepath.Join(e.directory, filename))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(e.directory, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("PDF now points outside the indexed directory")
	}
	return path, nil
}
