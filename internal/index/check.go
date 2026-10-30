package index

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/darkearl/semantic-search/internal/pdf"
)

// FileRefresh changes a file fingerprint only after its page text was verified.
type FileRefresh struct {
	Filename string `json:"filename"`
	Before   string `json:"before"`
	After    string `json:"after"`
}
type CheckResult struct {
	ChangedPages int           `json:"changed_pages"`
	AddedFiles   int           `json:"added_files"`
	RemovedFiles int           `json:"removed_files"`
	Errors       []string      `json:"errors"`
	Refresh      []FileRefresh `json:"refresh"`
}

// Check reads a snapshot without loading a model or writing the index. It keeps
// only one page's text at a time. A caller must discard results after a library
// switch or index update, and serialize any metadata save with its index writer.
func (idx *Index) Check(ctx context.Context) (CheckResult, error) {
	r := CheckResult{Errors: []string{}, Refresh: []FileRefresh{}}
	if err := idx.Validate(); err != nil {
		return r, err
	}
	docs := make(map[string]Document)
	known := make(map[string]bool)
	skipped := make(map[string]string)
	for _, d := range idx.Documents {
		docs[d.Filename] = d
		known[d.Filename] = true
	}
	for _, issue := range idx.Issues {
		if issue.Kind == "skipped" {
			known[issue.Filename] = true
			skipped[issue.Filename] = issue.SHA256
		}
	}
	seen := make(map[string]bool)
	err := filepath.WalkDir(idx.Root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".pdf") {
			return nil
		}
		rel, err := filepath.Rel(idx.Root, path)
		if err != nil {
			return err
		}
		seen[rel] = true
		if !known[rel] {
			r.AddedFiles++
			return nil
		}
		before, err := fileHash(ctx, path)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", rel, err))
			return ctx.Err()
		}
		old, indexed := docs[rel]
		if !indexed {
			if skipped[rel] != before {
				r.Errors = append(r.Errors, rel+": previously skipped PDF changed; update to retry")
			}
			return nil
		}
		if old.SHA256 == before {
			return nil
		}
		extractor, _ := pdf.New()
		defer extractor.Close()
		count, err := extractor.PageCount(ctx, path)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", rel, err))
			return ctx.Err()
		}
		changes := max(0, old.PageCount-count)
		healthy := true
		for page := 1; page <= count; page++ {
			text, err := extractor.PageText(ctx, path, page)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				healthy = false
				r.Errors = append(r.Errors, fmt.Sprintf("%s page %d: %v", rel, page, err))
				break
			}
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
			if page > len(old.PageTextSHA256) || old.PageTextSHA256[page-1] != digest {
				changes++
			}
		}
		after, err := fileHash(ctx, path)
		if err != nil {
			return err
		}
		if before != after {
			return fmt.Errorf("%s changed while checking; retry by selecting the library again", rel)
		}
		r.ChangedPages += changes
		if healthy && changes == 0 {
			r.Refresh = append(r.Refresh, FileRefresh{rel, old.SHA256, after})
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	for name := range known {
		if !seen[name] {
			r.RemovedFiles++
		}
	}
	return r, nil
}

// RefreshFiles preserves vectors and page hashes. The before-hash prevents a
// scan of an older snapshot from replacing metadata in a newer index.
func (idx *Index) RefreshFiles(files []FileRefresh) (*Index, error) {
	next := *idx
	next.Documents = append([]Document(nil), idx.Documents...)
	for _, f := range files {
		if !validSHA256(f.Before) || !validSHA256(f.After) {
			return nil, fmt.Errorf("invalid refresh hash")
		}
		found := false
		for i := range next.Documents {
			d := &next.Documents[i]
			if d.Filename == f.Filename {
				if d.SHA256 != f.Before {
					return nil, fmt.Errorf("index changed during check")
				}
				d.SHA256 = f.After
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("PDF is no longer indexed")
		}
	}
	return &next, nil
}
