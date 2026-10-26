// Package pdf reads PDF objects and extracts page text entirely in Go. It does
// not execute helper programs or depend on a third-party PDF parser.
package pdf

import (
	"context"
	"fmt"
	"os"
)

// Extractor keeps one file open and caches only document structure. Page content
// streams are decoded on demand. Each indexing worker owns its own Extractor;
// an individual Extractor must not be shared by concurrent calls.
type Extractor struct {
	path string
	doc  *document
}

func New() (*Extractor, error) { return &Extractor{}, nil }
func (e *Extractor) Close() error {
	if e.doc == nil {
		return nil
	}
	err := e.doc.file.Close()
	e.doc, e.path = nil, ""
	return err
}
func (e *Extractor) open(ctx context.Context, path string) (*document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.doc != nil && e.path == path {
		return e.doc, nil
	}
	if err := e.Close(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	d, err := openDocument(ctx, f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("read PDF: %w", err)
	}
	e.path, e.doc = path, d
	return d, nil
}
func (e *Extractor) PageCount(ctx context.Context, path string) (int, error) {
	// A new indexing pass may see a changed file at the same path.
	if err := e.Close(); err != nil {
		return 0, err
	}
	d, err := e.open(ctx, path)
	if err != nil {
		return 0, err
	}
	return len(d.pages), nil
}
func (e *Extractor) PageText(ctx context.Context, path string, page int) (string, error) {
	d, err := e.open(ctx, path)
	if err != nil {
		return "", err
	}
	if page < 1 || page > len(d.pages) {
		return "", fmt.Errorf("page %d out of range (1–%d)", page, len(d.pages))
	}
	return d.pageText(ctx, d.pages[page-1])
}
