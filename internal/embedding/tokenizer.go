package embedding

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const MaxTokens = 256

// Tokenizer implements the uncased BERT BasicTokenizer and WordPiece pipeline.
type Tokenizer struct {
	vocab         map[string]int64
	cls, sep, unk int64
}

func LoadTokenizer(path string) (*Tokenizer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadTokenizer(f)
}

// ReadTokenizer also accepts an embedded vocabulary without a filesystem copy.
func ReadTokenizer(r io.Reader) (*Tokenizer, error) {
	t := &Tokenizer{vocab: make(map[string]int64)}
	s := bufio.NewScanner(r)
	var id int64
	for s.Scan() {
		t.vocab[strings.TrimSuffix(s.Text(), "\r")] = id
		id++
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	for _, name := range []string{"[CLS]", "[SEP]", "[UNK]", "[PAD]", "[MASK]"} {
		if _, ok := t.vocab[name]; !ok {
			return nil, fmt.Errorf("vocabulary missing %s", name)
		}
	}
	t.cls, t.sep, t.unk = t.vocab["[CLS]"], t.vocab["[SEP]"], t.vocab["[UNK]"]
	return t, nil
}

func chinese(r rune) bool {
	return r >= 0x4e00 && r <= 0x9fff || r >= 0x3400 && r <= 0x4dbf ||
		r >= 0x20000 && r <= 0x2a6df || r >= 0x2a700 && r <= 0x2b73f ||
		r >= 0x2b740 && r <= 0x2b81f || r >= 0x2b820 && r <= 0x2ceaf ||
		r >= 0xf900 && r <= 0xfaff || r >= 0x2f800 && r <= 0x2fa1f
}

func punctuation(r rune) bool {
	return r >= 33 && r <= 47 || r >= 58 && r <= 64 || r >= 91 && r <= 96 ||
		r >= 123 && r <= 126 || unicode.IsPunct(r)
}

func basicTokens(text string) []string {
	var cleaned strings.Builder
	for _, r := range text {
		switch {
		case r == 0 || r == unicode.ReplacementChar:
			continue
		case r == '\t' || r == '\n' || r == '\r' || r == ' ' || unicode.Is(unicode.Zs, r):
			cleaned.WriteByte(' ')
		case unicode.Is(unicode.C, r):
			continue
		case chinese(r):
			cleaned.WriteByte(' ')
			cleaned.WriteRune(r)
			cleaned.WriteByte(' ')
		default:
			cleaned.WriteRune(r)
		}
	}
	var result []string
	for _, word := range strings.Fields(cleaned.String()) {
		var token strings.Builder
		flush := func() {
			if token.Len() > 0 {
				result = append(result, token.String())
				token.Reset()
			}
		}
		for _, r := range norm.NFD.String(strings.ToLower(word)) {
			if unicode.Is(unicode.Mn, r) {
				continue
			}
			if punctuation(r) {
				flush()
				result = append(result, string(r))
			} else {
				token.WriteRune(r)
			}
		}
		flush()
	}
	return result
}

func (t *Tokenizer) wordPieces(word string) []int64 {
	runes := []rune(word)
	if len(runes) > 100 {
		return []int64{t.unk}
	}
	var ids []int64
	for start := 0; start < len(runes); {
		found := false
		for end := len(runes); end > start; end-- {
			piece := string(runes[start:end])
			if start > 0 {
				piece = "##" + piece
			}
			if id, ok := t.vocab[piece]; ok {
				ids = append(ids, id)
				start = end
				found = true
				break
			}
		}
		if !found {
			return []int64{t.unk}
		}
	}
	return ids
}

// Windows never drops text. A page remains one index entry even when it needs
// several bounded model calls. Special tokens count toward the model limit.
func (t *Tokenizer) Windows(text string) [][]int64 {
	var ids []int64
	// Preserve BERT's added special tokens before lowercasing and punctuation splitting.
	for len(text) > 0 {
		pos, special := len(text), ""
		for _, candidate := range []string{"[CLS]", "[SEP]", "[UNK]", "[PAD]", "[MASK]"} {
			if i := strings.Index(text, candidate); i >= 0 && i < pos {
				pos, special = i, candidate
			}
		}
		for _, token := range basicTokens(text[:pos]) {
			ids = append(ids, t.wordPieces(token)...)
		}
		if special == "" {
			break
		}
		ids = append(ids, t.vocab[special])
		text = text[pos+len(special):]
	}
	var windows [][]int64
	for start := 0; start < len(ids); start += MaxTokens - 2 {
		end := min(start+MaxTokens-2, len(ids))
		window := make([]int64, 0, end-start+2)
		window = append(window, t.cls)
		window = append(window, ids[start:end]...)
		window = append(window, t.sep)
		windows = append(windows, window)
	}
	return windows
}
