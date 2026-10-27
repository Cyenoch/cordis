package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	wasisys "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

func TestReadStdinPreservesPartialDataAndBackpressure(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
		want wasisys.Errno
	}{
		{name: "empty pipe", want: wasisys.EAGAIN},
		{name: "short read", data: []byte("abc")},
		{name: "full first vector then EAGAIN", data: []byte("abcd")},
		{name: "two vectors", data: []byte("abcdef")},
	} {
		t.Run(test.name, func(t *testing.T) {
			memory := wazerotest.NewMemory(65536)
			memory.WriteUint32Le(0, 128)
			memory.WriteUint32Le(4, 4)
			memory.WriteUint32Le(8, 256)
			memory.WriteUint32Le(12, 4)
			pending := bytes.NewBuffer(test.data)
			errno := readStdin(memory, 0, 2, 32, func(buffer []byte) (int, wasisys.Errno) {
				if pending.Len() == 0 {
					return 0, wasisys.EAGAIN
				}
				n, _ := pending.Read(buffer)
				return n, 0
			})
			if errno != test.want {
				t.Fatalf("errno = %v, want %v", errno, test.want)
			}
			if errno != 0 {
				return
			}
			n, _ := memory.ReadUint32Le(32)
			got := append(append([]byte{}, memory.Bytes[128:132]...), memory.Bytes[256:260]...)
			if int(n) != len(test.data) || !bytes.Equal(got[:n], test.data) {
				t.Fatalf("read %d bytes: %q, want %q", n, got[:n], test.data)
			}
		})
	}
}

func TestReadStdinRejectsInvalidMemoryBeforeConsumingPipe(t *testing.T) {
	for _, test := range []struct {
		name                string
		iovs, count, result uint32
		second, length      uint32
		want                wasisys.Errno
	}{
		{name: "second buffer outside memory", count: 2, result: 32, second: 65535, length: 4, want: wasisys.EFAULT},
		{name: "vector count overflow", count: 1 << 29, result: 32, want: wasisys.EFAULT},
		{name: "table address overflow", iovs: ^uint32(0) - 3, count: 1, result: 32, want: wasisys.EFAULT},
		{name: "result outside memory", count: 1, result: 65535, want: wasisys.EFAULT},
		{name: "result overlaps table", count: 1, result: 4, want: wasisys.EINVAL},
		{name: "buffer overlaps table", count: 2, result: 32, second: 8, length: 4, want: wasisys.EINVAL},
		{name: "buffer overlaps result", count: 2, result: 32, second: 31, length: 4, want: wasisys.EINVAL},
	} {
		t.Run(test.name, func(t *testing.T) {
			memory := wazerotest.NewMemory(65536)
			binary.LittleEndian.PutUint32(memory.Bytes, 128)
			binary.LittleEndian.PutUint32(memory.Bytes[4:], 4)
			binary.LittleEndian.PutUint32(memory.Bytes[8:], test.second)
			binary.LittleEndian.PutUint32(memory.Bytes[12:], test.length)
			errno := readStdin(memory, test.iovs, test.count, test.result, func([]byte) (int, wasisys.Errno) {
				t.Fatal("invalid description consumed the host pipe")
				return 0, 0
			})
			if errno != test.want {
				t.Fatalf("errno = %v, want %v", errno, test.want)
			}
		})
	}
}

func TestReadStdinEOFAndZeroLengthVectors(t *testing.T) {
	memory := wazerotest.NewMemory(65536)
	memory.WriteUint32Le(0, ^uint32(0))
	memory.WriteUint32Le(8, 128)
	memory.WriteUint32Le(12, 4)
	calls := 0
	errno := readStdin(memory, 0, 2, 32, func([]byte) (int, wasisys.Errno) {
		calls++
		return 0, 0
	})
	n, _ := memory.ReadUint32Le(32)
	if errno != 0 || n != 0 || calls != 1 {
		t.Fatalf("EOF: errno=%v n=%d calls=%d", errno, n, calls)
	}
}

func TestStdinReadProbeUsesRealGuestDescriptorTable(t *testing.T) {
	ctx := t.Context()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	guest, err := runtime.InstantiateWithConfig(ctx, wasiReadProbe(), wazero.NewModuleConfig().WithStdin(bytes.NewBufferString("probe")))
	if err != nil {
		t.Fatal(err)
	}
	guest.Memory().WriteUint32Le(0, 4096)
	guest.Memory().WriteUint32Le(4, 16)
	result, err := guest.ExportedFunction("fd_read").Call(ctx, 0, 0, 1, 32)
	if err != nil || result[0] != 0 {
		t.Fatalf("guest fd_read = %v / %v", result, err)
	}
	n, _ := guest.Memory().ReadUint32Le(32)
	data, _ := guest.Memory().Read(4096, n)
	if string(data) != "probe" {
		t.Fatalf("guest memory read %q", data)
	}
}

func TestStdinReadErrorsCrossWASIBoundary(t *testing.T) {
	for _, test := range []struct {
		name   string
		result uint32
		errno  wasisys.Errno
		want   uint64
	}{
		{name: "empty pipe remains retryable", result: 32, errno: wasisys.EAGAIN, want: 6},
		{name: "bad result address", result: 65535, want: 21},
		{name: "overlapping descriptor", result: 4, want: 28},
		{name: "host read failure", result: 32, errno: wasisys.EIO, want: 29},
		{name: "invalid handle", result: 32, errno: wasisys.EBADF, want: 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			runtime := wazero.NewRuntime(ctx)
			defer runtime.Close(ctx)
			builder := runtime.NewHostModuleBuilder(wasi_snapshot_preview1.ModuleName)
			wasi_snapshot_preview1.NewFunctionExporter().ExportFunctions(builder)
			builder.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, module api.Module, stack []uint64) {
				stack[0] = stdinWASIErrno(readStdin(module.Memory(), uint32(stack[1]), uint32(stack[2]), uint32(stack[3]), func([]byte) (int, wasisys.Errno) {
					return 0, test.errno
				}))
			}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).Export("fd_read")
			if _, err := builder.Instantiate(ctx); err != nil {
				t.Fatal(err)
			}
			guest, err := runtime.Instantiate(ctx, wasiReadProbe())
			if err != nil {
				t.Fatal(err)
			}
			guest.Memory().WriteUint32Le(0, 4096)
			guest.Memory().WriteUint32Le(4, 16)
			result, err := guest.ExportedFunction("fd_read").Call(ctx, 0, 0, 1, uint64(test.result))
			if err != nil || result[0] != test.want {
				t.Fatalf("WASI guest observed errno %v / %v, want %d", result, err, test.want)
			}
		})
	}
}

// Local wrappers call the WASI imports with the real guest as their caller.
// Directly re-exporting imports would give host calls no guest memory.
func wasiReadProbe() []byte {
	names := []string{"fd_read", "fd_fdstat_set_flags", "fd_close", "fd_fdstat_get"}
	params := []byte{4, 2, 1, 2}
	binary := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	uleb := func(n uint32) []byte {
		var encoded []byte
		for n >= 128 {
			encoded = append(encoded, byte(n)|128)
			n >>= 7
		}
		return append(encoded, byte(n))
	}
	text := func(s string) []byte { return append(uleb(uint32(len(s))), s...) }
	section := func(id byte, content []byte) {
		binary = append(binary, id)
		binary = append(binary, uleb(uint32(len(content)))...)
		binary = append(binary, content...)
	}
	types := []byte{byte(len(names))}
	imports := []byte{byte(len(names))}
	functions := []byte{byte(len(names))}
	code := []byte{byte(len(names))}
	exports := []byte{byte(len(names) + 1)}
	for i, name := range names {
		types = append(types, 0x60, params[i])
		for range params[i] {
			types = append(types, 0x7f)
		}
		types = append(types, 1, 0x7f)
		imports = append(imports, text(wasi_snapshot_preview1.ModuleName)...)
		imports = append(imports, text(name)...)
		imports = append(imports, 0, byte(i))
		functions = append(functions, byte(i))
		body := []byte{0} // No locals beyond function parameters.
		for param := range params[i] {
			body = append(body, 0x20, byte(param)) // local.get
		}
		body = append(body, 0x10, byte(i), 0x0b) // call import; end
		code = append(code, uleb(uint32(len(body)))...)
		code = append(code, body...)
		exports = append(exports, text(name)...)
		exports = append(exports, 0, byte(len(names)+i))
	}
	exports = append(exports, text("memory")...)
	exports = append(exports, 2, 0)
	section(1, types)
	section(2, imports)
	section(3, functions)
	section(5, []byte{1, 0, 1})
	section(7, exports)
	section(10, code)
	return binary
}
