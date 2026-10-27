package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"golang.org/x/sys/windows"
)

func TestWindowsStdinReadQueuedDataBeyondGuestBuffer(t *testing.T) {
	stdin, writer := windowsStdinPipe(t)
	guest := windowsReadGuest(t, stdin)
	callWASI(t, guest, "fd_fdstat_set_flags", 0, 4)
	payload := bytes.Repeat([]byte("bounded-stdin-"), 1500)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	memory := guest.Memory()
	memory.WriteUint32Le(0, 4096)
	memory.WriteUint32Le(4, 4096)
	var got []byte
	for len(got) < len(payload) {
		callWASI(t, guest, "fd_read", 0, 0, 1, 32)
		n, _ := memory.ReadUint32Le(32)
		if n == 0 || n > 4096 {
			t.Fatalf("queued pipe read = %d, want 1..4096", n)
		}
		data, _ := memory.Read(4096, n)
		got = append(got, data...)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("small reads lost or reordered queued input")
	}
	if errno := invokeWASI(t, guest, "fd_read", 0, 0, 1, 32); errno != 6 { // WASI AGAIN, not experimental/sys.EAGAIN.
		t.Fatalf("empty pipe errno = %d, want EAGAIN", errno)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	callWASI(t, guest, "fd_read", 0, 0, 1, 32)
	if n, _ := memory.ReadUint32Le(32); n != 0 {
		t.Fatalf("EOF returned %d bytes", n)
	}
}

func TestWindowsStdinReadPreservesFlagsCloseAndRejectedMemory(t *testing.T) {
	stdin, writer := windowsStdinPipe(t)
	guest := windowsReadGuest(t, stdin)
	memory := guest.Memory()
	memory.WriteUint32Le(0, 4096)
	memory.WriteUint32Le(4, 4)
	callWASI(t, guest, "fd_fdstat_set_flags", 0, 4)
	callWASI(t, guest, "fd_fdstat_get", 0, 64)
	if flags, _ := memory.ReadUint16Le(66); flags&4 == 0 {
		t.Fatalf("upstream fdstat lost NONBLOCK: %d", flags)
	}
	if errno := invokeWASI(t, guest, "fd_fdstat_set_flags", 0, 2); errno != 28 { // WASI INVAL
		t.Fatalf("unsupported DSYNC flag errno = %d", errno)
	}
	if errno := invokeWASI(t, guest, "fd_read", 0, 0, 1, 32); errno != 6 {
		t.Fatalf("failed flag update changed nonblock: %d", errno)
	}
	if _, err := writer.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if errno := invokeWASI(t, guest, "fd_read", 0, 0, 1, 65535); errno != 21 { // WASI FAULT
		t.Fatalf("invalid result pointer errno = %d", errno)
	}
	callWASI(t, guest, "fd_read", 0, 0, 1, 32)
	data, _ := memory.Read(4096, 4)
	if string(data) != "data" {
		t.Fatalf("invalid pointer consumed input: %q", data)
	}
	callWASI(t, guest, "fd_fdstat_set_flags", 0, 0)
	if _, err := writer.Write([]byte("next")); err != nil {
		t.Fatal(err)
	}
	callWASI(t, guest, "fd_read", 0, 0, 1, 32)
	callWASI(t, guest, "fd_close", 0)
	if errno := invokeWASI(t, guest, "fd_read", 0, 0, 1, 32); errno != 8 { // WASI BADF
		t.Fatalf("closed fd still readable: %d", errno)
	}
	if errno := invokeWASI(t, guest, "fd_read", 1, 0, 1, 32); errno != 8 {
		t.Fatalf("stdout became readable: %d", errno)
	}
}

func TestWindowsStdinReadRedirectedFileUsesUpstream(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "redirected-stdin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	if _, err := file.WriteString("file input"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	guest := windowsReadGuest(t, file)
	callWASI(t, guest, "fd_fdstat_set_flags", 0, 4)
	guest.Memory().WriteUint32Le(0, 4096)
	guest.Memory().WriteUint32Le(4, 16)
	callWASI(t, guest, "fd_read", 0, 0, 1, 32)
	n, _ := guest.Memory().ReadUint32Le(32)
	data, _ := guest.Memory().Read(4096, n)
	if string(data) != "file input" {
		t.Fatalf("redirected file: %q", data)
	}
}

func windowsStdinPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	var reader, writer windows.Handle
	if err := windows.CreatePipe(&reader, &writer, nil, 65536); err != nil {
		t.Fatal(err)
	}
	in := os.NewFile(uintptr(reader), "stdin-pipe")
	out := os.NewFile(uintptr(writer), "host-writer")
	t.Cleanup(func() { in.Close(); out.Close() })
	return in, out
}

func windowsReadGuest(t *testing.T, stdin *os.File) api.Module {
	t.Helper()
	ctx := t.Context()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	t.Cleanup(func() {
		if err := runtime.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	original, err := wasi_snapshot_preview1.NewBuilder(runtime).Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close(ctx)
	builder := runtime.NewHostModuleBuilder(wasi_snapshot_preview1.ModuleName)
	wasi_snapshot_preview1.NewFunctionExporter().ExportFunctions(builder)
	if err := overrideStdinRead(builder, original, stdin); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Instantiate(ctx); err != nil {
		t.Fatal(err)
	}
	guest, err := runtime.InstantiateWithConfig(ctx, wasiReadProbe(), wazero.NewModuleConfig().WithStdin(stdin))
	if err != nil {
		t.Fatal(err)
	}
	return guest
}

func invokeWASI(t *testing.T, guest api.Module, name string, params ...uint64) uint64 {
	t.Helper()
	result, err := guest.ExportedFunction(name).Call(t.Context(), params...)
	if err != nil {
		t.Fatalf("%s trapped: %v", name, err)
	}
	return result[0]
}

func callWASI(t *testing.T, guest api.Module, name string, params ...uint64) {
	t.Helper()
	if errno := invokeWASI(t, guest, name, params...); errno != 0 {
		t.Fatalf("%s errno = %d", name, errno)
	}
}
