package main

import (
	"bytes"
	"io"
	"sync"

	"github.com/Cyenoch/cordis/contract"
)

// syncWriter serializes the command's own diagnostics with plugin output,
// which arrives from the runtime's goroutines.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// forwardStderr writes what plugins write to stderr, each line behind its
// entry. A runtime panic arrives in many small reads, so line starts are
// tracked per activation rather than per record.
func forwardStderr(w io.Writer) func(contract.StderrRecord) error {
	var mu sync.Mutex
	midLine := map[string]bool{}
	return func(record contract.StderrRecord) error {
		mu.Lock()
		defer mu.Unlock()
		prefix := "[" + record.Owner.Entry + "] "
		open := midLine[record.Owner.Activation]
		var text bytes.Buffer
		for line := range bytes.Lines(record.Data) {
			if !open {
				text.WriteString(prefix)
			}
			text.Write(line)
			open = !bytes.HasSuffix(line, []byte("\n"))
		}
		if record.Truncated {
			if open {
				text.WriteByte('\n')
			}
			text.WriteString(prefix + "stderr truncated\n")
			open = false
		}
		if record.End && open {
			// The activation ended in the middle of a line.
			text.WriteByte('\n')
			open = false
		}
		if open {
			midLine[record.Owner.Activation] = true
		} else {
			delete(midLine, record.Owner.Activation)
		}
		_, err := w.Write(text.Bytes())
		return err
	}
}
