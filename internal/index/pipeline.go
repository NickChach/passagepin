package index

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/darkearl/semantic-search/internal/embedding"
	"github.com/darkearl/semantic-search/internal/pdf"
)

// Options bounds CPU work, open PDF readers, and queued page text. The embedder
// must support concurrent Embed calls when EmbeddingWorkers is greater than one.
type Options struct {
	ExtractionWorkers int
	EmbeddingWorkers  int
}

func DefaultOptions() Options {
	workers := min(2, runtime.GOMAXPROCS(0))
	return Options{ExtractionWorkers: workers, EmbeddingWorkers: workers}
}

func (o Options) Validate() error {
	if o.ExtractionWorkers < 0 || o.ExtractionWorkers > 32 || o.EmbeddingWorkers < 0 || o.EmbeddingWorkers > 32 {
		return fmt.Errorf("index worker counts must be between 1 and 32 (or 0 for defaults)")
	}
	return nil
}

// BuildConcurrent creates an independent extractor per file worker and closes
// each owned extractor before returning. No worker survives an error or cancel.
// A zero worker count selects the default; negative or excessive counts fail.
func BuildConcurrent(ctx context.Context, root, fingerprint string, recursive bool, old *Index, newExtractor func() (Extractor, error), embedder Embedder, progress io.Writer, emit func(Progress), options Options) (*Index, Stats, error) {
	if err := options.Validate(); err != nil {
		return nil, Stats{}, err
	}
	defaults := DefaultOptions()
	if options.ExtractionWorkers == 0 {
		options.ExtractionWorkers = defaults.ExtractionWorkers
	}
	if options.EmbeddingWorkers == 0 {
		options.EmbeddingWorkers = defaults.EmbeddingWorkers
	}
	var extractors []Extractor
	defer func() {
		for _, extractor := range extractors {
			if closer, ok := extractor.(io.Closer); ok {
				_ = closer.Close()
			}
		}
	}()
	for i := 0; i < options.ExtractionWorkers; i++ {
		if err := ctx.Err(); err != nil {
			return nil, Stats{}, err
		}
		extractor, err := newExtractor()
		if err != nil {
			return nil, Stats{}, err
		}
		extractors = append(extractors, extractor)
	}
	return buildPipeline(ctx, root, fingerprint, recursive, old, extractors, embedder, progress, emit, options.EmbeddingWorkers)
}

type pageJob struct {
	text     string
	page     *Page // Each job owns a distinct slot until its document's Wait completes.
	number   int
	filename string
	done     *sync.WaitGroup
	discard  *atomic.Bool
}

type documentResult struct {
	doc     Document
	reused  bool
	skipped *FileIssue
}

func fatalReadError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ENOMEM) || errors.Is(err, syscall.EIO)
}

func pageFailure(page int, err error) PageFailure {
	failure := PageFailure{Page: page, Reason: issueReason(err)}
	var feature *pdf.UnsupportedFeatureError
	if errors.As(err, &feature) {
		failure.Feature = feature.Feature
	}
	return failure
}

func issueReason(err error) string {
	runes := []rune(err.Error())
	if len(runes) > 1000 {
		return string(runes[:1000]) + "…"
	}
	return string(runes)
}

func recoveryInfo(extractor Extractor) string {
	if reporter, ok := extractor.(interface{ RecoveryInfo() string }); ok {
		return reporter.RecoveryInfo()
	}
	return ""
}

func buildPipeline(ctx context.Context, root, fingerprint string, recursive bool, old *Index, extractors []Extractor, embedder Embedder, progress io.Writer, emit func(Progress), embeddingWorkers int) (*Index, Stats, error) {
	started := time.Now()
	var stats Stats
	var outputMu sync.Mutex
	notify := func(p Progress) {
		outputMu.Lock()
		defer outputMu.Unlock()
		if emit != nil {
			emit(p)
		}
		if progress != nil {
			switch p.Phase {
			case "reused":
				fmt.Fprintf(progress, "Unchanged: %s\n", p.Filename)
			case "embedding":
				fmt.Fprintf(progress, "Indexing: %s page %d/%d\n", p.Filename, p.Page, p.Pages)
			}
		}
	}
	notify(Progress{Phase: "discovering"})
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}
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
		if old.Version != Version {
			return nil, stats, fmt.Errorf("incompatible index; use --rebuild")
		}
		if old.Root != root || old.Fingerprint != fingerprint {
			return nil, stats, fmt.Errorf("index belongs to another directory or model; use --rebuild")
		}
		for _, doc := range old.Documents {
			cache[doc.Filename] = doc
		}
	}
	idx := &Index{Version: Version, Root: root, Model: embedding.ModelID, Fingerprint: fingerprint, UpdatedAt: time.Now().UTC(), Documents: []Document{}}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var firstErr error
	var failOnce sync.Once
	fail := func(err error) {
		if err != nil {
			failOnce.Do(func() {
				// Cancellation can interrupt a parser read with a secondary PDF
				// syntax error. Preserve the operation's cancellation reason.
				firstErr = ctx.Err()
				if firstErr == nil {
					firstErr = err
				}
				cancel()
			})
		}
	}
	var hashing, extraction, embeddings atomic.Int64
	hash := func(path string) (string, error) {
		start := time.Now()
		digest, err := fileHash(ctx, path)
		hashing.Add(int64(time.Since(start)))
		return digest, err
	}
	files := make(chan string)
	// Backpressure bounds queued page strings to twice the inference worker count.
	pages := make(chan pageJob, 2*embeddingWorkers)
	documents := make(chan documentResult)
	skip := func(filename string, page int, err error, failures ...PageFailure) {
		if ctx.Err() != nil {
			fail(ctx.Err())
			return
		}
		if fatalReadError(err) {
			fail(err)
			return
		}
		if page > 0 && len(failures) == 0 {
			failures = []PageFailure{pageFailure(page, err)}
		}
		issue := FileIssue{Filename: filename, Kind: "skipped", Reason: issueReason(err), Page: page, Failures: failures}
		var unsupported *pdf.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			issue.Feature = unsupported.Feature
		}
		if len(failures) > 0 {
			issue.Feature = failures[0].Feature
		}
		notify(Progress{Phase: "skipped", Filename: filename, Page: page, Reason: issue.Reason})
		if progress != nil {
			outputMu.Lock()
			fmt.Fprintf(progress, "Skipped: %s", filename)
			if page > 0 {
				fmt.Fprintf(progress, " (page %d)", page)
			}
			fmt.Fprintf(progress, ": %s; no pages from this PDF were indexed\n", issue.Reason)
			outputMu.Unlock()
		}
		select {
		case documents <- documentResult{skipped: &issue}:
		case <-ctx.Done():
		}
	}
	var readers, models sync.WaitGroup
	for i := 0; i < embeddingWorkers; i++ {
		models.Add(1)
		go func() {
			defer models.Done()
			for job := range pages {
				// Drain on cancellation so readers waiting for their pages can exit.
				if ctx.Err() == nil && !job.discard.Load() {
					start := time.Now()
					vector, err := embedder.Embed(ctx, job.text)
					embeddings.Add(int64(time.Since(start)))
					if err == nil {
						err = validateVector(vector)
					}
					switch {
					case errors.Is(err, embedding.ErrNoTokens):
						// Leave the slot empty, just like whitespace-only text. The
						// document compacts these slots and counts them as EmptyPages.
					case err != nil:
						fail(fmt.Errorf("%s page %d: %w", job.filename, job.number, err))
					default:
						*job.page = Page{Number: job.number, Vector: vector}
					}
				}
				job.done.Done()
			}
		}()
	}
	for _, extractor := range extractors {
		readers.Add(1)
		go func(extractor Extractor) {
			defer readers.Done()
			for path := range files {
				if ctx.Err() != nil {
					return
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					fail(err)
					return
				}
				digest, err := hash(path)
				if err != nil {
					skip(rel, 0, err)
					continue
				}
				if doc, ok := cache[rel]; ok && doc.SHA256 == digest {
					notify(Progress{Phase: "reused", Filename: rel, Pages: doc.PageCount, Recovered: doc.Recovery != "", Partial: len(doc.Failures) > 0})
					select {
					case documents <- documentResult{doc: doc, reused: true}:
					case <-ctx.Done():
						return
					}
					continue
				}
				start := time.Now()
				count, err := extractor.PageCount(ctx, path)
				extraction.Add(int64(time.Since(start)))
				if err != nil {
					skip(rel, 0, err)
					continue
				}
				doc := Document{Filename: rel, SHA256: digest, PageCount: count, Pages: make([]Page, count)}
				var pending sync.WaitGroup
				var discard atomic.Bool
				var readErr error
				failedPage := 0
				for page := 1; page <= count && ctx.Err() == nil; page++ {
					notify(Progress{Phase: "embedding", Filename: rel, Page: page, Pages: count})
					start := time.Now()
					text, err := extractor.PageText(ctx, path, page)
					extraction.Add(int64(time.Since(start)))
					if err != nil {
						if ctx.Err() != nil || fatalReadError(err) {
							fail(err)
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
					case pages <- pageJob{text: text, page: &doc.Pages[page-1], number: page, filename: rel, done: &pending, discard: &discard}:
					case <-ctx.Done():
						pending.Done()
					}
				}
				// Includes inference before rehashing: source edits during embedding
				// must not be committed under the original digest.
				pending.Wait()
				if ctx.Err() != nil {
					return
				}
				if readErr != nil {
					if reason := recoveryInfo(extractor); reason != "" {
						readErr = fmt.Errorf("%w (cross-reference recovery succeeded; original issue: %s)", readErr, reason)
					}
					skip(rel, failedPage, readErr)
					continue
				}
				after, err := hash(path)
				if err != nil {
					skip(rel, 0, err)
					continue
				}
				if after != digest {
					skip(rel, 0, fmt.Errorf("source changed during indexing; retry this PDF"))
					continue
				}
				kept := doc.Pages[:0]
				for _, page := range doc.Pages {
					if page.Number != 0 {
						kept = append(kept, page)
					}
				}
				doc.Pages = kept
				doc.Recovery = recoveryInfo(extractor)
				if len(doc.Pages) == 0 && len(doc.Failures) > 0 {
					skip(rel, doc.Failures[0].Page, fmt.Errorf("%s", doc.Failures[0].Reason), doc.Failures...)
					continue
				}
				notify(Progress{Phase: "completed", Filename: rel, Page: count, Pages: count, Recovered: doc.Recovery != "", Partial: len(doc.Failures) > 0})
				select {
				case documents <- documentResult{doc: doc}:
				case <-ctx.Done():
					return
				}
			}
		}(extractor)
	}
	go func() {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
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
			// Do not follow file or directory symlinks outside the selected tree.
			if !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".pdf") {
				return nil
			}
			select {
			case files <- path:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		fail(err)
		close(files)
		readers.Wait()
		close(pages)
		models.Wait()
		close(documents)
	}()
	for result := range documents {
		if result.skipped != nil {
			stats.Skipped++
			stats.FailedPages += len(result.skipped.Failures)
			idx.Issues = append(idx.Issues, *result.skipped)
			continue
		}
		idx.Documents = append(idx.Documents, result.doc)
		if len(result.doc.Failures) > 0 {
			stats.Partial++
			stats.FailedPages += len(result.doc.Failures)
			idx.Issues = append(idx.Issues, FileIssue{Filename: result.doc.Filename, Kind: "partial", Reason: fmt.Sprintf("%d pages failed extraction", len(result.doc.Failures)), Page: result.doc.Failures[0].Page, IndexedPages: len(result.doc.Pages), Failures: result.doc.Failures})
			if progress != nil {
				outputMu.Lock()
				fmt.Fprintf(progress, "Partially indexed: %s; %d searchable pages; %d pages failed extraction\n", result.doc.Filename, len(result.doc.Pages), len(result.doc.Failures))
				outputMu.Unlock()
			}
		}
		if result.doc.Recovery != "" {
			stats.Recovered++
			idx.Issues = append(idx.Issues, FileIssue{Filename: result.doc.Filename, Kind: "recovered", Reason: issueReason(fmt.Errorf("%s", result.doc.Recovery))})
		}
		if result.reused {
			stats.Reused++
		} else {
			stats.Indexed++
		}
		stats.Pages += len(result.doc.Pages)
		stats.EmptyPages += result.doc.PageCount - len(result.doc.Pages) - len(result.doc.Failures)
	}
	stats.Timings = Timings{Hashing: time.Duration(hashing.Load()), Extraction: time.Duration(extraction.Load()), Embedding: time.Duration(embeddings.Load()), Total: time.Since(started)}
	if firstErr != nil {
		return nil, stats, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}
	// Completion order is nondeterministic; the persisted index remains stable.
	sort.Slice(idx.Documents, func(i, j int) bool { return idx.Documents[i].Filename < idx.Documents[j].Filename })
	sort.Slice(idx.Issues, func(i, j int) bool {
		if idx.Issues[i].Filename == idx.Issues[j].Filename {
			return idx.Issues[i].Kind < idx.Issues[j].Kind
		}
		return idx.Issues[i].Filename < idx.Issues[j].Filename
	})
	stats.Issues = idx.Issues
	return idx, stats, nil
}
