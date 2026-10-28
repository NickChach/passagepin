package index

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/darkearl/semantic-search/internal/pdf"
)

// documentWorker owns one extractor. The coordinator owns the channels and waits
// for all workers; process waits for every page job before releasing its slots.
type documentWorker struct {
	ctx        context.Context
	root       string
	cache      map[string]Document
	extractor  Extractor
	hash       func(string) (string, error)
	extraction *atomic.Int64
	pages      chan<- pageJob
	documents  chan<- documentResult
	notify     func(Progress)
	fail       func(error)
	skip       func(string, int, error, ...PageFailure)
}

func (w *documentWorker) process(path string) {
	if w.ctx.Err() != nil {
		return
	}
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		w.fail(err)
		return
	}
	digest, err := w.hash(path)
	if err != nil {
		w.skip(rel, 0, err)
		return
	}
	if doc, ok := w.cache[rel]; ok && doc.SHA256 == digest {
		w.notify(Progress{Phase: "reused", Filename: rel, Pages: doc.PageCount, Recovered: doc.Recovery != "", Partial: len(doc.Failures) > 0})
		select {
		case w.documents <- documentResult{doc: doc, reused: true}:
		case <-w.ctx.Done():
			return
		}
		return
	}
	start := time.Now()
	count, err := w.extractor.PageCount(w.ctx, path)
	w.extraction.Add(int64(time.Since(start)))
	if err != nil {
		w.skip(rel, 0, err)
		return
	}
	doc := Document{Filename: rel, SHA256: digest, PageCount: count, Pages: make([]Page, count)}
	var pending sync.WaitGroup
	var discard atomic.Bool
	var readErr error
	failedPage := 0
	for page := 1; page <= count && w.ctx.Err() == nil; page++ {
		w.notify(Progress{Phase: "embedding", Filename: rel, Page: page, Pages: count})
		start := time.Now()
		text, err := w.extractor.PageText(w.ctx, path, page)
		w.extraction.Add(int64(time.Since(start)))
		if err != nil {
			if w.ctx.Err() != nil || fatalReadError(err) {
				w.fail(err)
				break
			}
			var structural *pdf.DocumentError
			if errors.As(err, &structural) {
				readErr, failedPage = err, page
				discard.Store(true)
				break
			}
			doc.Failures = append(doc.Failures, pageFailure(page, err))
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		pending.Add(1)
		select {
		case w.pages <- pageJob{text: text, page: &doc.Pages[page-1], number: page, filename: rel, done: &pending, discard: &discard}:
		case <-w.ctx.Done():
			pending.Done()
		}
	}
	// Includes inference before rehashing: source edits during embedding
	// must not be committed under the original digest.
	pending.Wait()
	if w.ctx.Err() != nil {
		return
	}
	if readErr != nil {
		if reason := recoveryInfo(w.extractor); reason != "" {
			readErr = fmt.Errorf("%w (cross-reference recovery succeeded; original issue: %s)", readErr, reason)
		}
		w.skip(rel, failedPage, readErr)
		return
	}
	after, err := w.hash(path)
	if err != nil {
		w.skip(rel, 0, err)
		return
	}
	if after != digest {
		w.skip(rel, 0, fmt.Errorf("source changed during indexing; retry this PDF"))
		return
	}
	kept := doc.Pages[:0]
	for _, page := range doc.Pages {
		if page.Number != 0 {
			kept = append(kept, page)
		}
	}
	doc.Pages = kept
	doc.Recovery = recoveryInfo(w.extractor)
	if len(doc.Pages) == 0 && len(doc.Failures) > 0 {
		w.skip(rel, doc.Failures[0].Page, fmt.Errorf("%s", doc.Failures[0].Reason), doc.Failures...)
		return
	}
	w.notify(Progress{Phase: "completed", Filename: rel, Page: count, Pages: count, Recovered: doc.Recovery != "", Partial: len(doc.Failures) > 0})
	select {
	case w.documents <- documentResult{doc: doc}:
	case <-w.ctx.Done():
		return
	}
}
