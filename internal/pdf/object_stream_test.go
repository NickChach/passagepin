package pdf

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func objectStreamDocument(t *testing.T, header, body string, count int64, compressed bool) *document {
	t.Helper()
	data := []byte(header + body)
	options := dict{"Type": name("ObjStm"), "N": count, "First": int64(len(header))}
	if compressed {
		data = flate(t, data)
		options["Filter"] = name("FlateDecode")
	}
	s := &stream{dict: options, data: data}
	return &document{ctx: context.Background(), xref: map[int64]xrefEntry{
		1: {kind: 1}, 10: {kind: 2, objectStream: 1},
		11: {kind: 2, objectStream: 1, index: 1}, 12: {kind: 2, objectStream: 1, index: 2},
	}, cache: map[reference]value{{1, 0}: s}, active: map[reference]bool{}}
}

func TestObjectStreamOutOfOrderMembersAndEviction(t *testing.T) {
	d := objectStreamDocument(t, "10 0 11 4 12 8 ", "(a) (b) (c)", 3, true)
	for _, number := range []int64{11, 10, 12, 11} {
		v, err := d.resolveObject(reference{number, 0})
		want := string(rune('a' + number - 10))
		if text, ok := v.(pdfString); err != nil || !ok || string(text) != want {
			t.Fatalf("member %d: %v, %v", number, v, err)
		}
	}
	first := d.objectStream
	other := objectStreamDocument(t, "10 0 11 4 12 8 ", "(x) (y) (z)", 3, true)
	d.cache[reference{2, 0}] = other.cache[reference{1, 0}]
	d.xref[2] = xrefEntry{kind: 1}
	d.xref[20] = xrefEntry{kind: 2, objectStream: 2, index: 1}
	// A mismatched xref member must still fail when the header is cached.
	for i := 0; i < 2; i++ {
		if _, err := d.resolveObject(reference{20, 0}); err == nil || err.Error() != "wrong object stream member" {
			t.Fatalf("wrong member: %v", err)
		}
	}
	if d.objectStream == first {
		t.Fatal("previous stream was retained")
	}
	v, err := d.resolveObject(reference{10, 0})
	if err != nil || string(v.(pdfString)) != "a" {
		t.Fatalf("revisit evicted stream: %v, %v", v, err)
	}
}

func TestObjectStreamMalformedSuffixRetries(t *testing.T) {
	for _, header := range []string{"10 0 11 nope ", "10 0 11 ", "10 0 11 -1 "} {
		d := objectStreamDocument(t, header, "(a) (b)", 2, true)
		if _, err := d.resolveObject(reference{10, 0}); err != nil {
			t.Fatal(err)
		}
		var message string
		for i := 0; i < 2; i++ {
			_, err := d.resolveObject(reference{11, 0})
			if err == nil {
				t.Fatal("malformed member accepted")
			}
			if i == 0 {
				message = err.Error()
			} else if message != err.Error() {
				t.Fatalf("retry changed error: %q -> %q", message, err)
			}
		}
		if _, err := d.resolveObject(reference{10, 0}); err != nil {
			t.Fatalf("unused malformed suffix rejected valid member: %v", err)
		}
	}
}

func TestObjectStreamCacheLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		size   int
		count  int64
		cached bool
	}{
		{"at limits", maxCachedObjectStream, maxCachedObjectEntries, true},
		{"large bytes", maxCachedObjectStream + 1, 3, false},
		{"many entries", 64, maxCachedObjectEntries + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := "10 0 11 4 12 8 "
			body := "(a) (b) (c)"
			body += strings.Repeat(" ", tc.size-len(header)-len(body))
			d := objectStreamDocument(t, header, body, tc.count, false)
			for _, number := range []int64{12, 10, 11, 12} {
				v, err := d.resolveObject(reference{number, 0})
				want := string(rune('a' + number - 10))
				if text, ok := v.(pdfString); err != nil || !ok || string(text) != want {
					t.Fatalf("member %d: %v, %v", number, v, err)
				}
				if (d.objectStream != nil) != tc.cached {
					t.Fatalf("cached=%t, want %t", d.objectStream != nil, tc.cached)
				}
			}
		})
	}
}

func TestObjectStreamCachedValidationAndCancellation(t *testing.T) {
	d := objectStreamDocument(t, "10 0 11 100 ", "(a)", 2, true)
	if _, err := d.resolveObject(reference{10, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.resolveObject(reference{11, 0}); err == nil || err.Error() != "object stream member out of range" {
		t.Fatalf("bad offset: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.ctx = ctx
	if _, err := d.resolveObject(reference{10, 0}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached member ignored cancellation: %v", err)
	}
}
