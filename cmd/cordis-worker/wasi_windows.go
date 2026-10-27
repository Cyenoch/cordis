package main

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"syscall"
	"unsafe"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	wasisys "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"golang.org/x/sys/windows"
)

const (
	wasiFault   = 21
	wasiInvalid = 28
	wasiIO      = 29
)

// wazero's Windows pipe poll reports EIO for a closed writer. WASI requires
// a readable hangup event, allowing Go's poller to read EOF and finish normally.
func instantiateWASI(ctx context.Context, runtime wazero.Runtime) error {
	original, err := wasi_snapshot_preview1.NewBuilder(runtime).Compile(ctx)
	if err != nil {
		return err
	}
	defer original.Close(ctx)
	definition := original.ExportedFunctions()["poll_oneoff"]
	poll, ok := definition.GoFunction().(api.GoModuleFunction)
	if !ok {
		return errors.New("WASI poll function is unavailable")
	}
	builder := runtime.NewHostModuleBuilder(wasi_snapshot_preview1.ModuleName)
	wasi_snapshot_preview1.NewFunctionExporter().ExportFunctions(builder)
	if err := overrideStdinRead(builder, original, os.Stdin); err != nil {
		return err
	}
	builder.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, module api.Module, stack []uint64) {
		input, output, count, result := uint32(stack[0]), uint32(stack[1]), uint32(stack[2]), uint32(stack[3])
		if count > ^uint32(0)/48 {
			stack[0] = wasiFault
			return
		}
		overlaps := func(a, n, b, m uint64) bool { return a < b+m && b < a+n }
		if overlaps(uint64(input), uint64(count)*48, uint64(output), uint64(count)*32) || overlaps(uint64(input), uint64(count)*48, uint64(result), 4) || overlaps(uint64(output), uint64(count)*32, uint64(result), 4) {
			stack[0] = wasiInvalid
			return
		}
		poll.Call(ctx, module, stack)
		if stack[0] != wasiIO || !stdinHangup() {
			return
		}
		memory := module.Memory()
		subscriptions, valid := memory.Read(input, count*48)
		if !valid {
			return
		}
		events, valid := memory.Read(output, count*32)
		if !valid {
			return
		}
		var ready uint32
		for index := uint32(0); index < count; index++ {
			subscription := subscriptions[index*48:]
			if subscription[8] != 1 || binary.LittleEndian.Uint32(subscription[16:]) != 0 {
				continue
			}
			event := events[ready*32:][:32]
			clear(event)
			copy(event, subscription[:8])
			event[10] = 1                                // FD_READ
			binary.LittleEndian.PutUint16(event[24:], 1) // FD_READWRITE_HANGUP
			ready++
		}
		if ready != 0 && memory.WriteUint32Le(result, ready) {
			stack[0] = 0
		}
	}), definition.ParamTypes(), definition.ResultTypes()).Export("poll_oneoff")
	_, err = builder.Instantiate(ctx)
	return err
}

var peekStdinPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")

// Keep wazero's file table and poll path, replacing only its unsafe nonblocking
// pipe read. Track flags/close only after the original host function succeeds.
func overrideStdinRead(builder wazero.HostModuleBuilder, original wazero.CompiledModule, stdin *os.File) error {
	info, err := stdin.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		return nil
	}
	var nonblock, closed bool
	for _, name := range []string{"fd_read", "fd_fdstat_set_flags", "fd_close"} {
		definition := original.ExportedFunctions()[name]
		function, ok := definition.GoFunction().(api.GoModuleFunction)
		if !ok {
			return errors.New("WASI stdin function is unavailable")
		}
		builder.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, module api.Module, stack []uint64) {
			fd := uint32(stack[0])
			if name == "fd_read" && fd == 0 && nonblock && !closed {
				stack[0] = stdinWASIErrno(readStdin(module.Memory(), uint32(stack[1]), uint32(stack[2]), uint32(stack[3]), func(buffer []byte) (int, wasisys.Errno) {
					return readStdinPipe(stdin, buffer)
				}))
				return
			}
			var flags uint16
			if name == "fd_fdstat_set_flags" {
				flags = uint16(stack[1])
			}
			function.Call(ctx, module, stack)
			if fd == 0 && stack[0] == 0 {
				switch name {
				case "fd_fdstat_set_flags":
					nonblock = flags&4 != 0 // WASI FD_NONBLOCK
				case "fd_close":
					closed = true
				}
			}
		}), definition.ParamTypes(), definition.ResultTypes()).Export(name)
	}
	return nil
}

func readStdinPipe(stdin *os.File, buffer []byte) (int, wasisys.Errno) {
	if len(buffer) == 0 {
		return 0, 0
	}
	var available uint32
	ok, _, err := peekStdinPipe.Call(stdin.Fd(), 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0)
	if ok == 0 {
		if errors.Is(err, windows.ERROR_BROKEN_PIPE) {
			return 0, 0
		}
		return 0, wasisys.UnwrapOSError(err)
	}
	if available == 0 {
		return 0, wasisys.EAGAIN
	}
	n, err := syscall.Read(syscall.Handle(stdin.Fd()), buffer[:min(uint64(len(buffer)), uint64(available))])
	if errors.Is(err, windows.ERROR_BROKEN_PIPE) {
		return 0, 0
	}
	return n, wasisys.UnwrapOSError(err)
}

func stdinHangup() bool {
	var available uint32
	_, _, err := peekStdinPipe.Call(os.Stdin.Fd(), 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0)
	return errors.Is(err, windows.ERROR_BROKEN_PIPE)
}
