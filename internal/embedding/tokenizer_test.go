package embedding

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testTokenizer(t *testing.T) *Tokenizer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vocab.txt")
	const vocab = "[PAD]\n[UNK]\n[CLS]\n[SEP]\n[MASK]\nhello\n,\nworld\n!\ncafe\nplay\n##ing\n中\n文\n$\n"
	if err := os.WriteFile(path, []byte(vocab), 0600); err != nil {
		t.Fatal(err)
	}
	tok, err := LoadTokenizer(path)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestBERTTokenization(t *testing.T) {
	tok := testTokenizer(t)
	cases := []struct {
		text string
		want []int64
	}{
		{"Héllo, WORLD!", []int64{2, 5, 6, 7, 8, 3}},
		{"Café playing 中文 $", []int64{2, 9, 10, 11, 12, 13, 14, 3}},
		{"unknown", []int64{2, 1, 3}},
		{"hello\x00\u200bworld", []int64{2, 1, 3}},
		{"[MASK] hello", []int64{2, 4, 5, 3}},
		{strings.Repeat("a", 101), []int64{2, 1, 3}},
	}
	for _, tt := range cases {
		got := tok.Windows(tt.text)
		if len(got) != 1 || !reflect.DeepEqual(got[0], tt.want) {
			t.Errorf("%q: got %v want %v", tt.text, got, tt.want)
		}
	}
	if got := tok.Windows(" \n\t"); len(got) != 0 {
		t.Fatalf("empty text: %v", got)
	}
}

func TestLongPageKeepsTail(t *testing.T) {
	tok := testTokenizer(t)
	windows := tok.Windows(strings.Repeat("hello ", 600) + "world")
	if len(windows) != 3 {
		t.Fatalf("got %d windows", len(windows))
	}
	count := 0
	for _, window := range windows {
		if len(window) > MaxTokens || window[0] != tok.cls || window[len(window)-1] != tok.sep {
			t.Fatalf("invalid window: %v", window)
		}
		count += len(window) - 2
	}
	last := windows[len(windows)-1]
	if count != 601 || last[len(last)-2] != 7 {
		t.Fatalf("lost tail: count=%d last=%v", count, last)
	}
}
