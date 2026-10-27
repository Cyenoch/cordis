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

func (t *Transport) Read(p []byte) (int, error)  { return t.Reader.Read(p) }
func (t *Transport) Write(p []byte) (int, error) { return t.Writer.Write(p) }
func (t *Transport) Close() error {
	t.once.Do(func() {
		for _, closer := range t.Closers {
			t.err = errors.Join(t.err, closer.Close())
		}
	})
	return t.err
}
