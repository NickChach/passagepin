package index

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
)

// walkPDFs defines library membership for both builds and checks. Visiting one
// file at a time preserves the pipeline's backpressure and cancellation behavior.
func walkPDFs(ctx context.Context, root string, recursive bool, visit func(string) error) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
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
		// File and directory symlinks are excluded from the selected tree.
		if !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".pdf") {
			return nil
		}
		return visit(path)
	})
}
