package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func breakStartXref(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.LastIndex(data, []byte("startxref"))
	data = append(append([]byte{}, data[:at]...), []byte("startxref\n0\n%%EOF\n")...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRecoverBadStartXrefWithoutChangingSource(t *testing.T) {
	path := fixture(t, basicObjects(streamBody([]byte("BT /F1 10 Tf (recovered text) Tj ET"), "")))
	before := breakStartXref(t, path)
	e, _ := New()
	defer e.Close()
	if count, err := e.PageCount(context.Background(), path); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if e.RecoveryInfo() == "" {
		t.Fatal("recovery was not reported")
	}
	if text, err := e.PageText(context.Background(), path, 1); err != nil || text != "recovered text" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("recovery rewrote the source")
	}
}

func TestRecoverLazyContentOffset(t *testing.T) {
	path := fixture(t, basicObjects(streamBody([]byte("BT /F1 10 Tf (lazy recovery) Tj ET"), "")))
	data, _ := os.ReadFile(path)
	at := bytes.Index(data, []byte("xref\n0 6\n"))
	lines := bytes.Split(data[at:], []byte("\n"))
	copy(lines[7][:10], "0000000000") // Content object 5 only; page tree remains readable.
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	e, _ := New()
	defer e.Close()
	if _, err := e.PageCount(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if e.RecoveryInfo() != "" {
		t.Fatal("healthy page tree unexpectedly triggered recovery")
	}
	if text, err := e.PageText(context.Background(), path, 1); err != nil || text != "lazy recovery" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if e.RecoveryInfo() == "" {
		t.Fatal("lazy recovery was not reported")
	}
}

func TestRecoverySkipsBinaryStreamContents(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 10 Tf (real page) Tj ET"), ""))
	fake := []byte("\x00\xff\nxref\n0 1\n0000000000 65535 f\ntrailer << /Encrypt 12 0 R >>\n999 0 obj << /Type /Catalog >> endobj\nstartxref\n0\n%%EOF\n")
	objects = append(objects, streamBody(fake, "/Type /XObject /Subtype /Image"))
	path := fixture(t, objects)
	breakStartXref(t, path)
	e, _ := New()
	defer e.Close()
	if text, err := e.PageText(context.Background(), path, 1); err != nil || text != "real page" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if _, exists := e.doc.xref[999]; exists {
		t.Fatal("invented an object from binary stream contents")
	}
}

func TestRecoveryUsesLatestRevisionAndHonorsFreeEntries(t *testing.T) {
	for _, freed := range []bool{false, true} {
		t.Run(fmt.Sprint(freed), func(t *testing.T) {
			path := fixture(t, basicObjects(streamBody([]byte("BT /F1 10 Tf (old edition) Tj ET"), "")))
			data, _ := os.ReadFile(path)
			var out bytes.Buffer
			out.Write(bytes.TrimRight(data, "\r\n \t")) // An appender can omit the separator after %%EOF.
			offset := out.Len()
			if !freed {
				fmt.Fprintf(&out, "5 0 obj\n%s\nendobj\n", streamBody([]byte("BT /F1 10 Tf (new edition) Tj ET"), ""))
			}
			xref := out.Len()
			entry := fmt.Sprintf("%010d 00000 n", offset)
			if freed {
				entry = "0000000000 00001 f"
			}
			fmt.Fprintf(&out, "xref\n5 1\n%s \ntrailer\n<< /Size 6 /Root 1 0 R /Prev 0 >>\nstartxref\n%d\n%%%%EOF\n", entry, xref)
			if err := os.WriteFile(path, out.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			e, _ := New()
			defer e.Close()
			text, err := e.PageText(context.Background(), path, 1)
			if freed {
				if err == nil || e.doc == nil || e.doc.xref[5].kind != 0 {
					t.Fatal("resurrected a deleted content object")
				}
			} else if err != nil || text != "new edition" {
				t.Fatalf("text=%q err=%v", text, err)
			}
		})
	}
}

func TestRecoveryRejectsUncertainStreamsAndOversizedFiles(t *testing.T) {
	for _, kind := range []string{"indirect-length", "bad-length", "object-stream", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			objects := basicObjects(streamBody([]byte("BT /F1 10 Tf (text) Tj ET"), ""))
			switch kind {
			case "indirect-length":
				objects[4] = "<< /Length 6 0 R >>\nstream\nBT ET\nendstream"
				objects = append(objects, "6")
			case "bad-length":
				objects[4] = "<< /Length 1 >>\nstream\nBT ET\nendstream"
			case "object-stream":
				objects = append(objects, streamBody([]byte(""), "/Type /ObjStm /N 0 /First 0"))
			}
			path := fixture(t, objects)
			breakStartXref(t, path)
			if kind == "oversized" {
				if err := os.Truncate(path, maxRecoveryBytes+1); err != nil {
					t.Fatal(err)
				}
			}
			e, _ := New()
			defer e.Close()
			if _, err := e.PageCount(context.Background(), path); err == nil {
				t.Fatal("accepted unsupported recovery case")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e, _ := New()
	defer e.Close()
	if _, err := e.PageCount(ctx, "unused.pdf"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored cancellation: %v", err)
	}
}

func TestRecoveryRefusesUncommittedTrailingObjects(t *testing.T) {
	path := fixture(t, basicObjects(streamBody([]byte("BT ET"), "")))
	data := breakStartXref(t, path)
	data = append(data, []byte("99 0 obj\nnull\nendobj\n")...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	e, _ := New()
	defer e.Close()
	if _, err := e.PageCount(context.Background(), path); err == nil || !strings.Contains(err.Error(), "recovery") {
		t.Fatalf("accepted incomplete revision: %v", err)
	}
}

func TestRecoveryDoesNotGuessAmongDuplicateObjects(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			path := fixture(t, basicObjects(streamBody([]byte("BT /F1 10 Tf (original) Tj ET"), "")))
			data, _ := os.ReadFile(path)
			at := bytes.Index(data, []byte("xref\n"))
			extra := []byte(fmt.Sprintf("5 0 obj\n%s\nendobj\n", streamBody([]byte("BT /F1 10 Tf (unreferenced) Tj ET"), "")))
			data = append(append(append([]byte{}, data[:at]...), extra...), data[at:]...)
			if ambiguous {
				lines := bytes.Split(data[at+len(extra):], []byte("\n"))
				copy(lines[7][:10], "0000000000")
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			breakStartXref(t, path)
			e, _ := New()
			defer e.Close()
			text, err := e.PageText(context.Background(), path, 1)
			if ambiguous {
				if err == nil || !strings.Contains(err.Error(), "ambiguous duplicate") {
					t.Fatalf("guessed a duplicate: %v", err)
				}
			} else if err != nil || text != "original" {
				t.Fatalf("ignored valid original object offset: text=%q err=%v", text, err)
			}
		})
	}
}

func TestRecoveryRejectsEncryptionAndHonorsCancellation(t *testing.T) {
	path := fixture(t, basicObjects(streamBody([]byte("BT ET"), "")))
	data, _ := os.ReadFile(path)
	data = bytes.Replace(data, []byte("/Root 1 0 R"), []byte("/Root 1 0 R /Encrypt 6 0 R"), 1)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	breakStartXref(t, path)
	e, _ := New()
	defer e.Close()
	if _, err := e.PageCount(context.Background(), path); err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("ignored encryption: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := recoverDocument(ctx, file, errors.New("xref")); !errors.Is(err, context.Canceled) {
		t.Fatalf("recovery ignored cancellation: %v", err)
	}
}

func TestExternalRecoveryStructure(t *testing.T) {
	path := os.Getenv("PDFSEARCH_TEST_RECOVERY_PDF")
	if path == "" {
		t.Skip("set PDFSEARCH_TEST_RECOVERY_PDF for an independent damaged PDF")
	}
	e, _ := New()
	defer e.Close()
	count, err := e.PageCount(context.Background(), path)
	if err != nil || count == 0 || e.RecoveryInfo() == "" {
		t.Fatalf("count=%d recovery=%q err=%v", count, e.RecoveryInfo(), err)
	}
	t.Logf("Recovered %d physical pages; original issue: %s", count, e.RecoveryInfo())
}
