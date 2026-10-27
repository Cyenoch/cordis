package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	go func() {
		time.Sleep(20 * time.Millisecond)
		fmt.Println("idle")
	}()
	hash := sha256.New()
	// Hide os.File.WriteTo so the requested guest read buffer is actually used.
	n, err := io.CopyBuffer(hash, struct{ io.Reader }{os.Stdin}, make([]byte, 4096))
	if err != nil {
		os.Exit(2)
	}
	fmt.Printf("%d %x\n", n, hash.Sum(nil))
}
