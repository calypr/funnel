package task

import (
	"io"
	"sort"
)

// jsonPositionReader records line starts in the stream while the JSON decoder reads it.
type jsonPositionReader struct {
	reader     io.Reader
	size       int64
	firstLine  int
	lineStarts []int64
}

func newJSONPositionReader(reader io.Reader) *jsonPositionReader {
	return &jsonPositionReader{reader: reader, firstLine: 1, lineStarts: []int64{0}}
}

func (r *jsonPositionReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	for i, b := range p[:n] {
		if b == '\n' {
			r.lineStarts = append(r.lineStarts, r.size+int64(i)+1)
		}
	}
	r.size += int64(n)
	return n, err
}

// position uses a zero-based byte offset and returns a one-based line and byte column.
func (r *jsonPositionReader) position(offset int64) (int, int64) {
	i := sort.Search(len(r.lineStarts), func(i int) bool { return r.lineStarts[i] > offset }) - 1
	return r.firstLine + i, offset - r.lineStarts[i] + 1
}

// discardBefore retains the current line and any data the decoder read ahead.
func (r *jsonPositionReader) discardBefore(offset int64) {
	i := sort.Search(len(r.lineStarts), func(i int) bool { return r.lineStarts[i] > offset }) - 1
	if i > 0 {
		r.firstLine += i
		r.lineStarts = append([]int64(nil), r.lineStarts[i:]...)
	}
}
