package pdf

import (
	"bytes"
	"fmt"
)

const (
	maxCachedObjectStream  = 4 << 20
	maxCachedObjectEntries = 4096
)

type objectStreamPosition struct{ number, offset int64 }

// Only the most recently used small object stream is retained. The cache belongs
// to one open document, so closing or recovering that document releases it.
type objectStreamCache struct {
	stream       *stream
	data         []byte
	count, first int64
	header       *syntax
	positions    []objectStreamPosition
}

func (d *document) objectStreamMember(s *stream, count, first, index, number int64) (value, error) {
	if err := d.ctx.Err(); err != nil {
		return nil, err
	}
	c := d.objectStream
	if c == nil || c.stream != s || c.count != count || c.first != first {
		d.objectStream = nil
		data, err := d.decode(s)
		if err != nil {
			return nil, err
		}
		if first < 0 || first > int64(len(data)) {
			return nil, fmt.Errorf("invalid object stream offset")
		}
		c = &objectStreamCache{stream: s, data: data, count: count, first: first, header: newSyntax(bytes.NewReader(data[:first]), 0)}
		if len(data) <= maxCachedObjectStream && count <= maxCachedObjectEntries {
			c.positions = make([]objectStreamPosition, 0, int(count))
			d.objectStream = c
		}
	}
	position, err := c.position(d, index)
	if err != nil {
		// A failed read advances the header parser. Retry malformed data from its
		// beginning rather than retaining that partially consumed state.
		d.objectStream = nil
		return nil, err
	}
	if position.number != number {
		return nil, fmt.Errorf("wrong object stream member")
	}
	if position.offset >= int64(len(c.data))-first {
		return nil, fmt.Errorf("object stream member out of range")
	}
	return newSyntax(bytes.NewReader(c.data[first+position.offset:]), 0).value(0)
}

func (c *objectStreamCache) position(d *document, index int64) (objectStreamPosition, error) {
	if index < int64(len(c.positions)) {
		return c.positions[index], nil
	}
	var position objectStreamPosition
	// Read only the requested prefix. Malformed unused members must not prevent
	// reading earlier valid members. Oversized streams use this same path without
	// retaining either the decoded bytes or header entries between calls.
	for i := int64(len(c.positions)); i <= index; i++ {
		if err := d.ctx.Err(); err != nil {
			return position, err
		}
		id, err := c.header.next()
		if err != nil {
			return position, err
		}
		off, err := c.header.next()
		if err != nil {
			return position, err
		}
		number, ok := integer(id)
		offset, okOffset := integer(off)
		if !ok || !okOffset || offset < 0 {
			return position, fmt.Errorf("invalid object stream index")
		}
		position = objectStreamPosition{number, offset}
		if c.positions != nil {
			c.positions = append(c.positions, position)
		}
	}
	return position, nil
}
