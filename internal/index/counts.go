package index

// Counts describes the snapshot, independent of how many PDFs a build reused.
type Counts struct {
	Documents, Pages, EmptyPages, FailedPages int
	Recovered, Partial, Skipped               int
}

func (idx *Index) Counts() Counts {
	counts := Counts{Documents: len(idx.Documents)}
	for _, doc := range idx.Documents {
		counts.Pages += len(doc.Pages)
		counts.EmptyPages += doc.PageCount - len(doc.Pages) - len(doc.Failures)
		counts.FailedPages += len(doc.Failures)
		if len(doc.Failures) > 0 {
			counts.Partial++
		}
		if doc.Recovery != "" {
			counts.Recovered++
		}
	}
	for _, issue := range idx.Issues {
		if issue.Kind == "skipped" {
			counts.Skipped++
			counts.FailedPages += len(issue.Failures)
		}
	}
	return counts
}
