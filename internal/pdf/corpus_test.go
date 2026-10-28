package pdf

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractionCorpus(t *testing.T) {
	var manifest struct {
		License string `json:"license"`
		Cases   []struct {
			File     string   `json:"file"`
			Features []string `json:"features"`
			Pages    []struct {
				Text       string `json:"text"`
				Error      string `json:"error_contains"`
				Feature    string `json:"unsupported_feature"`
				Limitation string `json:"known_limitation"`
			} `json:"pages"`
		} `json:"cases"`
	}
	data, err := os.ReadFile("testdata/corpus/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("testdata/corpus/*.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.License != "MIT" || len(manifest.Cases) == 0 || len(files) != len(manifest.Cases) {
		t.Fatal("corpus must have provenance and an expectation for every fixture")
	}
	seen := map[string]bool{}
	for _, tc := range manifest.Cases {
		if !filepath.IsLocal(tc.File) || filepath.Ext(tc.File) != ".pdf" || seen[tc.File] || len(tc.Features) == 0 || len(tc.Pages) == 0 {
			t.Fatalf("invalid corpus entry: %+v", tc)
		}
		seen[tc.File] = true
		t.Run(tc.File, func(t *testing.T) {
			e, _ := New()
			defer e.Close()
			path := filepath.Join("testdata/corpus", tc.File)
			count, err := e.PageCount(context.Background(), path)
			if err != nil || count != len(tc.Pages) {
				t.Fatalf("page count: %d, %v", count, err)
			}
			for i, want := range tc.Pages {
				got, err := e.PageText(context.Background(), path, i+1)
				if want.Error != "" {
					if err == nil || !strings.Contains(err.Error(), want.Error) || got != "" {
						t.Fatalf("page %d: got %q, %v; want explicit error %q and no partial text", i+1, got, err, want.Error)
					}
					var feature *UnsupportedFeatureError
					if want.Feature != "" && (!errors.As(err, &feature) || feature.Feature != want.Feature) {
						t.Fatalf("wrong unsupported feature: %v", err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("page %d: %v", i+1, err)
				}
				if want.Limitation != "" {
					// Preserve every word, including repeats, without blessing the
					// current reading order as the desired layout interpretation.
					counts := map[string]int{}
					for _, word := range strings.Fields(want.Text) {
						counts[word]++
					}
					for _, word := range strings.Fields(got) {
						counts[word]--
					}
					for word, n := range counts {
						if n != 0 {
							t.Fatalf("page %d: word %q count differs by %d", i+1, word, n)
						}
					}
					if got != want.Text {
						t.Logf("known limitation: %s; got %q; target %q", want.Limitation, got, want.Text)
					}
				} else if got != want.Text {
					t.Fatalf("page %d: got %q, want %q", i+1, got, want.Text)
				}
			}
		})
	}
}
