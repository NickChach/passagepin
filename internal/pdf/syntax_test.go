package pdf

import (
	"bytes"
	"io"
	"testing"
)

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
