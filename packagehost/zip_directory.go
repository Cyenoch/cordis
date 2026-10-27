package packagehost

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
)

// archive/zip builds its entire index and accepts wrapped EOCD counts. Bound
// both the advertised allocation and actual central records before invoking it.
func checkZIPDirectory(ctx context.Context, r io.ReaderAt, size int64, limit int) error {
	var end []byte
	var endOffset int64
	for _, window := range []int64{1024, 65 * 1024} {
		window = min(window, size)
		buf := make([]byte, int(window))
		if _, err := r.ReadAt(buf, size-window); err != nil {
			return err
		}
		for i := len(buf) - 22; i >= 0; i-- {
			if binary.LittleEndian.Uint32(buf[i:]) != 0x06054b50 {
				continue
			}
			if i+22+int(binary.LittleEndian.Uint16(buf[i+20:])) > len(buf) {
				break
			}
			end, endOffset = buf[i:], size-window+int64(i)
			break
		}
		if end != nil || window == size {
			break
		}
	}
	if end == nil {
		return zip.ErrFormat
	}
	records := uint64(binary.LittleEndian.Uint16(end[10:]))
	length := uint64(binary.LittleEndian.Uint32(end[12:]))
	offset := uint64(binary.LittleEndian.Uint32(end[16:]))
	if records == 0xffff || length == 0xffffffff || offset == 0xffffffff {
		var locator [20]byte
		if endOffset >= 20 {
			if _, err := r.ReadAt(locator[:], endOffset-20); err != nil {
				return err
			}
			if binary.LittleEndian.Uint32(locator[:]) == 0x07064b50 &&
				binary.LittleEndian.Uint32(locator[4:]) == 0 &&
				binary.LittleEndian.Uint32(locator[16:]) == 1 {
				position := binary.LittleEndian.Uint64(locator[8:])
				if position > uint64(size) || uint64(size)-position < 56 {
					return zip.ErrFormat
				}
				var end64 [56]byte
				if _, err := r.ReadAt(end64[:], int64(position)); err != nil {
					return err
				}
				if binary.LittleEndian.Uint32(end64[:]) != 0x06064b50 {
					return zip.ErrFormat
				}
				endOffset = int64(position)
				records = binary.LittleEndian.Uint64(end64[32:])
				length = binary.LittleEndian.Uint64(end64[40:])
				offset = binary.LittleEndian.Uint64(end64[48:])
			}
		}
	}
	if records > uint64(limit) {
		return fmt.Errorf("archive entry quota exceeded")
	}
	if length > uint64(endOffset) || offset > uint64(size) {
		return zip.ErrFormat
	}
	start := endOffset - int64(length)
	if err := scanZIPRecords(ctx, r, start, size, limit); err != nil {
		return err
	}
	// archive/zip may prefer the unadjusted offset for prefixed archives. Check
	// that alternative too, rather than relying on its header-count heuristics.
	if start > int64(offset) {
		return scanZIPRecords(ctx, r, int64(offset), size, limit)
	}
	return nil
}

func scanZIPRecords(ctx context.Context, r io.ReaderAt, offset, size int64, limit int) error {
	var header [46]byte
	for count := 0; offset < size; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.ReadAt(header[:], offset)
		if n < 4 || binary.LittleEndian.Uint32(header[:]) != 0x02014b50 {
			return nil // archive/zip validates the terminating record and counts.
		}
		if count == limit {
			return fmt.Errorf("archive entry quota exceeded")
		}
		if err != nil {
			return err
		}
		length := int64(len(header)) + int64(binary.LittleEndian.Uint16(header[28:])) +
			int64(binary.LittleEndian.Uint16(header[30:])) + int64(binary.LittleEndian.Uint16(header[32:]))
		if length > size-offset {
			return io.ErrUnexpectedEOF
		}
		offset += length
	}
	return nil
}
