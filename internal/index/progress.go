package index

import (
	"fmt"
	"io"
	"sync"
)

// buildObserver separates indexing outcomes from their presentation. Calls may
// originate from concurrent readers, so adapters serialize their consumers.
type buildObserver interface {
	event(Progress)
	issue(FileIssue)
}

type progressReporter struct {
	mu     sync.Mutex
	output io.Writer
	emit   func(Progress)
}

func (r *progressReporter) event(p Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.emit != nil {
		r.emit(p)
	}
	if r.output == nil {
		return
	}
	switch p.Phase {
	case "reused":
		fmt.Fprintf(r.output, "Unchanged: %s\n", p.Filename)
	case "embedding":
		fmt.Fprintf(r.output, "Indexing: %s page %d/%d\n", p.Filename, p.Page, p.Pages)
	}
}
func (r *progressReporter) issue(issue FileIssue) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.output == nil {
		return
	}
	switch issue.Kind {
	case "skipped":
		fmt.Fprintf(r.output, "Skipped: %s", issue.Filename)
		if issue.Page > 0 {
			fmt.Fprintf(r.output, " (page %d)", issue.Page)
		}
		fmt.Fprintf(r.output, ": %s; no pages from this PDF were indexed\n", issue.Reason)
	case "partial":
		fmt.Fprintf(r.output, "Partially indexed: %s; %d searchable pages; %d pages failed extraction\n", issue.Filename, issue.IndexedPages, len(issue.Failures))
	}
}
