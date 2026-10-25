package pdf

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/darkearl/semantic-search/internal/testpdf"
)

func TestPhysicalPageNumbers(t *testing.T) {
	t.Setenv("PATH", "") // Extraction must work without external executables.
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	path := filepath.Join(t.TempDir(), "spaces and (symbols).pdf")
	pages := []string{"First page: garden plants.", "", "Third page: renewable electricity."}
	if err := testpdf.Write(path, pages); err != nil {
		t.Fatal(err)
	}
	n, err := e.PageCount(context.Background(), path)
	if err != nil || n != 3 {
		t.Fatalf("count %d, error %v", n, err)
	}
	for i, want := range pages {
		got, err := e.PageText(context.Background(), path, i+1)
		if err != nil || got != want {
			t.Fatalf("page %d: got %q, want %q, error %v", i+1, got, want, err)
		}
	}
}
