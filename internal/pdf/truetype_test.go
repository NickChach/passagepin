package pdf

import (
	"context"
	"encoding/binary"
	"testing"
)

func ttFormat4(code, gid uint16) []byte {
	words := []uint16{4, 32, 0, 4, 4, 1, 0, code, 0xffff, 0, code, 0xffff, gid - code, 1, 0, 0}
	data := make([]byte, len(words)*2)
	for i, word := range words {
		binary.BigEndian.PutUint16(data[2*i:], word)
	}
	return data
}

func ttFormat12(groups ...[3]uint32) []byte {
	data := make([]byte, 16+12*len(groups))
	binary.BigEndian.PutUint16(data, 12)
	binary.BigEndian.PutUint32(data[4:], uint32(len(data)))
	binary.BigEndian.PutUint32(data[12:], uint32(len(groups)))
	for i, group := range groups {
		for j, n := range group {
			binary.BigEndian.PutUint32(data[16+12*i+4*j:], n)
		}
	}
	return data
}

func ttFixture(tables ...[]byte) []byte {
	cmap := make([]byte, 4+8*len(tables))
	binary.BigEndian.PutUint16(cmap[2:], uint16(len(tables)))
	for i, table := range tables {
		record := cmap[4+8*i:]
		binary.BigEndian.PutUint16(record, 3)
		encoding := uint16(1)
		if binary.BigEndian.Uint16(table) == 12 {
			encoding = 10
		}
		binary.BigEndian.PutUint16(record[2:], encoding)
		binary.BigEndian.PutUint32(record[4:], uint32(len(cmap)))
		cmap = append(cmap, table...)
	}
	font := make([]byte, 28)
	binary.BigEndian.PutUint32(font, 0x00010000)
	binary.BigEndian.PutUint16(font[4:], 1)
	copy(font[12:], "cmap")
	binary.BigEndian.PutUint32(font[20:], uint32(len(font)))
	binary.BigEndian.PutUint32(font[24:], uint32(len(cmap)))
	return append(font, cmap...)
}

func TestTrueTypeUnicodeReverseMapping(t *testing.T) {
	font := ttFixture(ttFormat4('A', 7), ttFormat12([3]uint32{'A', 'A', 7}, [3]uint32{0x1f600, 0x1f600, 9}))
	got, err := trueTypeUnicode(font)
	if err != nil || got[7] != "A" || got[9] != "😀" || len(got) != 2 {
		t.Fatalf("reverse Unicode: %v, %v", got, err)
	}
	// Two characters mapped to one glyph are not an invertible text encoding.
	got, err = trueTypeUnicode(ttFixture(ttFormat12([3]uint32{'A', 'A', 7}, [3]uint32{'B', 'B', 7}, [3]uint32{0xf041, 0xf041, 8})))
	if err != nil || len(got) != 0 {
		t.Fatalf("guessed ambiguous/private-use mappings: %v, %v", got, err)
	}
}

func TestTrueTypeGlyphArrayAndMalformedTables(t *testing.T) {
	table := ttFormat4('A', 7)
	// Use a glyphIdArray rather than an idDelta segment for A.
	binary.BigEndian.PutUint16(table[2:], 34)
	binary.BigEndian.PutUint16(table[24:], 2)
	binary.BigEndian.PutUint16(table[28:], 4)
	table = append(table, 0, 5) // 5 + delta 2 = glyph 7.
	got, err := trueTypeUnicode(ttFixture(table))
	if err != nil || got[7] != "A" {
		t.Fatalf("glyph array: %v, %v", got, err)
	}
	valid := ttFixture(table)
	for length := 0; length < len(valid); length++ {
		if _, err := trueTypeUnicode(valid[:length]); err == nil {
			t.Fatalf("accepted font truncated at %d", length)
		}
	}
	for _, bad := range [][]byte{
		ttFormat12([3]uint32{10, 9, 1}),
		ttFormat12([3]uint32{0x110000, 0x110001, 1}),
		ttFormat12([3]uint32{1, 65536, 1}),
		ttFormat12([3]uint32{1, 2, 1}, [3]uint32{2, 3, 4}),
	} {
		if _, err := trueTypeUnicode(ttFixture(bad)); err == nil {
			t.Fatal("accepted invalid cmap group")
		}
	}
	table[28], table[29] = 0xff, 0xfe
	if _, err := trueTypeUnicode(ttFixture(table)); err == nil {
		t.Fatal("accepted out-of-bounds glyph array")
	}
}

func TestEmbeddedCIDFontRecoveryRespectsGlyphMapAndToUnicode(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		objects := basicObjects(streamBody([]byte("BT /F1 12 Tf <00010002> Tj ET"), ""))
		unicodeEntry := ""
		if explicit {
			unicodeEntry = "/ToUnicode 10 0 R"
		}
		objects[3] = "<< /Type /Font /Subtype /Type0 /Encoding /Identity-H /DescendantFonts [6 0 R] " + unicodeEntry + " >>"
		objects = append(objects,
			"<< /Type /Font /Subtype /CIDFontType2 /FontDescriptor 7 0 R /CIDToGIDMap 9 0 R >>",
			"<< /Type /FontDescriptor /FontFile2 8 0 R >>",
			streamBody(ttFixture(ttFormat4('A', 7), ttFormat12([3]uint32{0x1f600, 0x1f600, 9})), ""),
			streamBody([]byte{0, 0, 0, 7, 0, 9}, ""),
			streamBody([]byte("1 beginbfchar <0001> <0042> endbfchar"), ""))
		want := "A😀"
		if explicit {
			want = "B😀"
		}
		if got := extract(t, fixture(t, objects), 1); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		// A CID with no entry in the glyph map must not be treated as Unicode.
		objects[4] = streamBody([]byte("BT /F1 12 Tf <00010003> Tj ET"), "")
		e, _ := New()
		got, err := e.PageText(context.Background(), fixture(t, objects), 1)
		e.Close()
		if err == nil || got != "" {
			t.Fatalf("guessed an unmapped CID: %q, %v", got, err)
		}
	}
}
