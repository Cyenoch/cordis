package process

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func removeActivationDirectory(path string) error {
	// Windows can retain executable-image mappings briefly after process/job
	// completion. Retry only the two observed file-in-use errors, then report it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.RemoveAll(path)
		if err == nil || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION)) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
