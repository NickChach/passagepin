package pdf

import (
	"bytes"
	"errors"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestTruncatedCompositeSyntax(t *testing.T) {
	for _, raw := range []string{
		"[", "[(after)", "[1 [2", "[1 % unfinished comment",
		"<<", "<< /Key", "<< /Key (after)", "<< /Key [1",
		"(unfinished\\",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := newSyntax(bytes.NewBufferString(raw), 0).value(0)
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("expected unexpected EOF for %q, got %v", raw, err)
			}
		})
	}
	for _, raw := range []string{"", " \n% final comment"} {
		if _, err := newSyntax(bytes.NewBufferString(raw), 0).value(0); err != io.EOF {
			t.Fatalf("clean EOF for %q: %v", raw, err)
		}
	}
}

func TestPDFStringSyntax(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`(nested (parentheses) and \(escaped\))`, "nested (parentheses) and (escaped)"},
		{"(line\\\r\ncontinuation)", "linecontinuation"},
		{`(octal \101\102\103)`, "octal ABC"},
		{`<4142 4>`, "AB@"},
	}
	for _, tc := range cases {
		p := newSyntax(bytes.NewBufferString(tc.raw), 0)
		v, err := p.value(0)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := v.(pdfString)
		if !ok || string(got) != tc.want {
			t.Errorf("%s: got %q want %q", tc.raw, got, tc.want)
		}
	}
	p := newSyntax(bytes.NewBufferString(`/A#20B`), 0)
	v, err := p.value(0)
	if err != nil || v != name("A B") {
		t.Fatalf("name escape: %v %v", v, err)
	}
}
func TestType1FontProgramEncoding(t *testing.T) {
	enc, err := type1Encoding([]byte("%!PS-AdobeFont-1.0\n/Encoding 256 array 0 1 255 {1 index exch /.notdef put} for dup 65 /A put dup 66 /epsilon1 put readonly def currentfile eexec"))
	if err != nil || enc[65] != "A" || enc[66] != "ϵ" {
		t.Fatalf("Type 1 encoding: %v %v", enc, err)
	}
}
func FuzzPDFSyntax(f *testing.F) {
	for _, seed := range []string{`<< /Length 3 0 R /Key [(abc) <ff>] >>`, `(nested (abc) \101)`, `[1 2 3]`, `/Name#20With#20Spaces`, `<< /X null >>`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		p := newSyntax(bytes.NewReader(data), 0)
		for i := 0; i < 4096; i++ {
			_, err := p.value(0)
			if err == io.EOF || err != nil {
				return
			}
		}
	})
}

// Compare keyword/number classification with the original conversion order,
// including overflow and nonfinite spellings that must remain keywords.
func FuzzPDFTokenClassification(f *testing.F) {
	for _, token := range []string{"BT", "n", "false", "true", "null", "NaN", "Inf", "Infinity", "+Inf", "-Inf", "42", "-3", "+.5", ".25", "1e2", "1e999", "9223372036854775808", "0x1p2", "+", "."} {
		f.Add(token)
	}
	f.Fuzz(func(t *testing.T, token string) {
		if token == "" || len(token) > 1024 {
			t.Skip()
		}
		for i := 0; i < len(token); i++ {
			if delimiter(token[i]) {
				t.Skip()
			}
		}
		var want value = keyword(token)
		if n, err := strconv.ParseInt(token, 10, 64); err == nil {
			want = n
		} else if n, err := strconv.ParseFloat(token, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			want = n
		} else {
			switch token {
			case "true":
				want = true
			case "false":
				want = false
			case "null":
				want = nil
			}
		}
		got, err := newSyntax(strings.NewReader(token), 0).next()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: got %T(%v), %v; want %T(%v)", token, got, got, err, want, want)
		}
	})
}
