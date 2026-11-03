package embedding

import (
	"bufio"
	"context"
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

func basicTokensContext(ctx context.Context, text string) ([]string, error) {
	var cleaned strings.Builder
	nextCheck := 0
	for i, r := range text {
		if i >= nextCheck {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			nextCheck = i + 4096
		}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var token strings.Builder
		flush := func() {
			if token.Len() > 0 {
				result = append(result, token.String())
				token.Reset()
			}
		}
		nextCheck = 0
		for i, r := range norm.NFD.String(strings.ToLower(word)) {
			if i >= nextCheck {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				nextCheck = i + 4096
			}
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
	return result, ctx.Err()
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
	windows, _ := t.WindowsContext(context.Background(), text)
	return windows
}

// WindowsContext preserves the same tokens while allowing long pages to stop
// during tokenization, before inference begins.
func (t *Tokenizer) WindowsContext(ctx context.Context, text string) ([][]int64, error) {
	var ids []int64
	appendText := func(fragment string) error {
		tokens, err := basicTokensContext(ctx, fragment)
		if err != nil {
			return err
		}
		for _, token := range tokens {
			if err := ctx.Err(); err != nil {
				return err
			}
			ids = append(ids, t.wordPieces(token)...)
		}
		return nil
	}
	// Visit each byte once; only '[' can begin an added BERT special token.
	start, nextCheck := 0, 0
	for pos := 0; pos < len(text); pos++ {
		if pos >= nextCheck {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			nextCheck = pos + 4096
		}
		if text[pos] != '[' {
			continue
		}
		for _, special := range []string{"[CLS]", "[SEP]", "[UNK]", "[PAD]", "[MASK]"} {
			if !strings.HasPrefix(text[pos:], special) {
				continue
			}
			if err := appendText(text[start:pos]); err != nil {
				return nil, err
			}
			ids = append(ids, t.vocab[special])
			pos += len(special) - 1
			start = pos + 1
			break
		}
	}
	if err := appendText(text[start:]); err != nil {
		return nil, err
	}
	var windows [][]int64
	for start := 0; start < len(ids); start += MaxTokens - 2 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+MaxTokens-2, len(ids))
		window := make([]int64, 0, end-start+2)
		window = append(window, t.cls)
		window = append(window, ids[start:end]...)
		window = append(window, t.sep)
		windows = append(windows, window)
	}
	return windows, ctx.Err()
}
