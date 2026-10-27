package packagehost

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func TestZIPDirectoryAcceptsBoundedVariants(t *testing.T) {
	for _, kind := range []string{"ordinary", "long-comment", "prefixed", "zip64"} {
		t.Run(kind, func(t *testing.T) {
			var data bytes.Buffer
			writer := zip.NewWriter(&data)
			if kind == "long-comment" {
				if err := writer.SetComment(strings.Repeat("x", 65535)); err != nil {
					t.Fatal(err)
				}
			}
			entry, err := writer.Create("file")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = entry.Write([]byte("payload")); err != nil {
				t.Fatal(err)
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			raw := data.Bytes()
			if kind == "prefixed" {
				raw = append([]byte("prefix"), raw...)
			}
			if kind == "zip64" {
				end := append([]byte(nil), raw[len(raw)-22:]...)
				var end64 [56]byte
				binary.LittleEndian.PutUint32(end64[:], 0x06064b50)
				binary.LittleEndian.PutUint64(end64[4:], 44)
				binary.LittleEndian.PutUint64(end64[24:], 1)
				binary.LittleEndian.PutUint64(end64[32:], 1)
				binary.LittleEndian.PutUint64(end64[40:], uint64(binary.LittleEndian.Uint32(end[12:])))
				binary.LittleEndian.PutUint64(end64[48:], uint64(binary.LittleEndian.Uint32(end[16:])))
				var locator [20]byte
				binary.LittleEndian.PutUint32(locator[:], 0x07064b50)
				binary.LittleEndian.PutUint64(locator[8:], uint64(len(raw)-22))
				binary.LittleEndian.PutUint32(locator[16:], 1)
				binary.LittleEndian.PutUint16(end[10:], 0xffff)
				raw = append(raw[:len(raw)-22], end64[:]...)
				raw = append(raw, locator[:]...)
				raw = append(raw, end...)
			}
			r := bytes.NewReader(raw)
			if err := checkZIPDirectory(context.Background(), r, int64(len(raw)), 1); err != nil {
				t.Fatal(err)
			}
			parsed, err := zip.NewReader(r, int64(len(raw)))
			if err != nil || len(parsed.File) != 1 {
				t.Fatal("invalid positive fixture", err)
			}
		})
	}
}

type countingReaderAt struct {
	io.ReaderAt
	read int
}

func (r *countingReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, offset)
	r.read += n
	return n, err
}

func TestZIPDirectoryBoundsActualRecordsBeforeIndexing(t *testing.T) {
	for _, kind := range []string{"wrapped-count", "forged-count", "zip64"} {
		t.Run(kind, func(t *testing.T) {
			const count = 65537
			var data bytes.Buffer
			var header [47]byte
			binary.LittleEndian.PutUint32(header[:], 0x02014b50)
			binary.LittleEndian.PutUint16(header[28:], 1)
			header[46] = 'x'
			for range count {
				data.Write(header[:])
			}
			var end [22]byte
			binary.LittleEndian.PutUint32(end[:], 0x06054b50)
			binary.LittleEndian.PutUint16(end[10:], 1)
			binary.LittleEndian.PutUint32(end[12:], uint32(data.Len()))
			if kind == "forged-count" {
				binary.LittleEndian.PutUint16(end[10:], 0)
			}
			if kind == "zip64" {
				var end64 [56]byte
				binary.LittleEndian.PutUint32(end64[:], 0x06064b50)
				binary.LittleEndian.PutUint64(end64[32:], 1) // Also distrust the ZIP64 record count.
				binary.LittleEndian.PutUint64(end64[40:], uint64(data.Len()))
				var locator [20]byte
				binary.LittleEndian.PutUint32(locator[:], 0x07064b50)
				binary.LittleEndian.PutUint64(locator[8:], uint64(data.Len()))
				binary.LittleEndian.PutUint32(locator[16:], 1)
				data.Write(end64[:])
				data.Write(locator[:])
				binary.LittleEndian.PutUint16(end[10:], 0xffff)
			}
			data.Write(end[:])
			r := &countingReaderAt{ReaderAt: bytes.NewReader(data.Bytes())}
			err := checkZIPDirectory(context.Background(), r, int64(data.Len()), 8)
			if err == nil || !strings.Contains(err.Error(), "entry quota exceeded") {
				t.Fatal("actual directory count was not bounded", err)
			}
			if r.read > 2048 {
				t.Fatalf("scanned unbounded records before rejecting: %d bytes", r.read)
			}
		})
	}
}
