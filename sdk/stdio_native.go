//go:build !wasip1

package sdk

import (
	"io"
	"os"

	"github.com/Cyenoch/cordis/wire"
)

func stdioTransport() (*wire.Transport, error) {
	return &wire.Transport{Reader: os.Stdin, Writer: os.Stdout, Closers: []io.Closer{os.Stdin}}, nil
}
