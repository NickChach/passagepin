// Package worker serves a persistent, serial search engine over JSON lines.
package worker

import (
	"context"
	"encoding/json"
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
		for _, issue := range e.idx.Issues {
			if issue.Kind == "skipped" {
				l.FailedPages += len(issue.Failures)
			}
		}
		l.Documents = len(e.idx.Documents)
		l.UpdatedAt = &e.idx.UpdatedAt
		for _, doc := range e.idx.Documents {
			l.Pages += len(doc.Pages)
			l.EmptyPages += doc.PageCount - len(doc.Pages) - len(doc.Failures)
			l.FailedPages += len(doc.Failures)
		}
	}
	return l
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	return json.Unmarshal(raw, v)
}

func (e *Engine) Handle(ctx context.Context, req Request, emit func(index.Progress)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch req.Method {
	case "status":
		return e.summary(), nil
	case "open":
		var p struct {
			Directory string `json:"directory"`
			IndexPath string `json:"index_path"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		if p.Directory == "" || p.IndexPath == "" {
			return nil, fmt.Errorf("directory and index_path are required")
		}
		root, err := filepath.Abs(p.Directory)
		if err != nil {
			return nil, err
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("select a directory of PDFs")
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
	case "index":
		if e.directory == "" {
			return nil, fmt.Errorf("choose a folder first")
		}
		var p struct {
			Rebuild bool `json:"rebuild"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		if e.issue != "" && !p.Rebuild {
			return nil, fmt.Errorf("rebuild the incompatible index")
		}
		old := e.idx
		if p.Rebuild {
			old = nil
		}
		idx, stats, err := index.BuildConcurrent(ctx, e.directory, e.fingerprint, true, old, func() (index.Extractor, error) { return pdf.New() }, e.model, io.Discard, emit, e.indexOptions)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		saving := time.Now()
		if err := idx.Save(e.indexPath); err != nil {
			return nil, err
		}
		stats.Timings.Saving = time.Since(saving)
		e.idx, e.issue = idx, ""
		return struct {
			Library Library     `json:"library"`
			Stats   index.Stats `json:"stats"`
		}{e.summary(), stats}, nil
	case "search":
		if e.idx == nil {
			return nil, fmt.Errorf("index this folder before searching")
		}
		var p struct {
			Query    string  `json:"query"`
			Top      int     `json:"top"`
			MinScore float64 `json:"min_score"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		p.Query = strings.TrimSpace(p.Query)
		if p.Query == "" || len(p.Query) > 16000 {
			return nil, fmt.Errorf("enter a subject of up to 16,000 bytes")
		}
		if p.Top == 0 {
			p.Top = 20
		}
		if p.Top < 1 || p.Top > 500 {
			return nil, fmt.Errorf("top must be between 1 and 500")
		}
		vector, err := e.model.Embed(ctx, p.Query)
		if err != nil {
			return nil, err
		}
		results, err := e.idx.SearchContext(ctx, vector, p.Top, p.MinScore)
		if err != nil {
			return nil, err
		}
		return struct {
			Results []index.Result `json:"results"`
			Query   string         `json:"query"`
		}{results, p.Query}, nil
	case "page":
		var p struct {
			Filename string `json:"filename"`
			Page     int    `json:"page"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		path, err := e.pagePath(p.Filename, p.Page)
		if err != nil {
			return nil, err
		}
		extractor, _ := pdf.New()
		defer extractor.Close()
		text, err := extractor.PageText(ctx, path, p.Page)
		if err != nil {
			return nil, err
		}
		// Keep IPC previews bounded; indexing still embeds the entire page.
		if len(text) > 200000 {
			text = text[:200000] + "\n\n[Preview shortened. Open the PDF to read the full page.]"
		}
		return struct {
			Filename string `json:"filename"`
			Page     int    `json:"page"`
			Text     string `json:"text"`
			Path     string `json:"path"`
		}{p.Filename, p.Page, text, path}, nil
	default:
		return nil, fmt.Errorf("unknown worker method %q", req.Method)
	}
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
