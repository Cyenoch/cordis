package sdk

import (
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/Cyenoch/cordis/wire"
)

func stdioTransport() (*wire.Transport, error) {
	if err := syscall.SetNonblock(0, true); err != nil {
		return nil, fmt.Errorf("set session stdin nonblocking: %w", err)
	}
	if err := syscall.SetNonblock(1, true); err != nil {
		return nil, fmt.Errorf("set session stdout nonblocking: %w", err)
	}
	input := os.NewFile(0, "cordis session stdin")
	output := os.NewFile(1, "cordis session stdout")
	return &wire.Transport{Reader: input, Writer: output, Closers: []io.Closer{input}}, nil
}
