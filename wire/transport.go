package wire

import (
	"errors"
	"io"
	"sync"
)

// Transport combines independently owned read and write streams.
type Transport struct {
	Reader  io.Reader
	Writer  io.Writer
	Closers []io.Closer
	once    sync.Once
	err     error
}

// Read reads from Reader.
func (t *Transport) Read(p []byte) (int, error) { return t.Reader.Read(p) }

// Write writes to Writer.
func (t *Transport) Write(p []byte) (int, error) { return t.Writer.Write(p) }

// Close closes every Closer once and returns their joined errors, also on later calls.
func (t *Transport) Close() error {
	t.once.Do(func() {
		for _, closer := range t.Closers {
			t.err = errors.Join(t.err, closer.Close())
		}
	})
	return t.err
}
