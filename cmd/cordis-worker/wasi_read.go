package main

import (
	"encoding/binary"
	"slices"

	"github.com/tetratelabs/wazero/api"
	wasisys "github.com/tetratelabs/wazero/experimental/sys"
)

// readStdin validates all guest destinations before consuming the host pipe.
func readStdin(memory api.Memory, iovs, count, result uint32, read func([]byte) (int, wasisys.Errno)) wasisys.Errno {
	tableSize := uint64(count) * 8
	if tableSize > uint64(^uint32(0)) {
		return wasisys.EFAULT
	}
	table, ok := memory.Read(iovs, uint32(tableSize))
	if !ok {
		return wasisys.EFAULT
	}
	if _, ok := memory.Read(result, 4); !ok {
		return wasisys.EFAULT
	}
	if readRegionsOverlap(iovs, uint32(tableSize), result, 4) {
		return wasisys.EINVAL
	}
	var capacity uint64
	for vector := range slices.Chunk(table, 8) {
		offset := binary.LittleEndian.Uint32(vector)
		size := binary.LittleEndian.Uint32(vector[4:])
		if size == 0 {
			continue
		}
		if _, ok := memory.Read(offset, size); !ok {
			return wasisys.EFAULT
		}
		if readRegionsOverlap(offset, size, iovs, uint32(tableSize)) || readRegionsOverlap(offset, size, result, 4) {
			return wasisys.EINVAL
		}
		capacity += uint64(size)
		if capacity > uint64(^uint32(0)) {
			return wasisys.EINVAL
		}
	}
	var total uint32
	for vector := range slices.Chunk(table, 8) {
		offset := binary.LittleEndian.Uint32(vector)
		size := binary.LittleEndian.Uint32(vector[4:])
		if size == 0 {
			continue
		}
		buffer, _ := memory.Read(offset, size) // Validated above; descriptors cannot be overwritten by reads.
		n, errno := read(buffer)
		if errno != 0 {
			if errno != wasisys.EAGAIN || total == 0 {
				return errno
			}
			break
		}
		total += uint32(n)
		if n < len(buffer) {
			break
		}
	}
	memory.WriteUint32Le(result, total)
	return 0
}

func readRegionsOverlap(a, sizeA, b, sizeB uint32) bool {
	return sizeA != 0 && sizeB != 0 && uint64(a) < uint64(b)+uint64(sizeB) && uint64(b) < uint64(a)+uint64(sizeA)
}

// experimental/sys.Errno is not the WASI preview 1 errno number space.
// Translate only custom host results; exported wazero functions already do this.
func stdinWASIErrno(errno wasisys.Errno) uint64 {
	switch errno {
	case 0:
		return 0
	case wasisys.EACCES:
		return 2
	case wasisys.EAGAIN:
		return 6
	case wasisys.EBADF:
		return 8
	case wasisys.EEXIST:
		return 20
	case wasisys.EFAULT:
		return 21
	case wasisys.EINTR:
		return 27
	case wasisys.EINVAL:
		return 28
	case wasisys.EISDIR:
		return 31
	case wasisys.ELOOP:
		return 32
	case wasisys.ENAMETOOLONG:
		return 37
	case wasisys.ENOENT:
		return 44
	case wasisys.ENOSYS:
		return 52
	case wasisys.ENOTDIR:
		return 54
	case wasisys.ENOTEMPTY:
		return 55
	case wasisys.ENOTSOCK:
		return 57
	case wasisys.ENOTSUP:
		return 58
	case wasisys.EPERM:
		return 63
	case wasisys.ERANGE:
		return 68
	case wasisys.EROFS:
		return 69
	default:
		return 29 // WASI IO, including experimental/sys.EIO.
	}
}
