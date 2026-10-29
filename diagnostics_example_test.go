package cordis_test

import (
	"context"
	"fmt"
	"os"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

func ExampleHost_Done() {
	directory, err := os.MkdirTemp("", "cordis-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(directory)
	h, err := cordis.Open(cordis.Options{Directory: directory})
	if err != nil {
		panic(err)
	}
	i, err := h.AddBuiltin(context.Background(), contract.Entry{ID: "broken"}, contract.Definition{
		Start: func(context.Context, contract.Scope) error {
			return fmt.Errorf("initialization failed")
		},
	})
	if err != nil {
		panic(err)
	}
	_ = i.Wait(context.Background())
	status := i.Status()
	fmt.Println(status.Identity.Entry, status.State, status.Outcome.FailureCode)
	fmt.Println("activation recorded:", status.Identity.Activation != "")
	fmt.Println("close returned error:", h.Close(context.Background()) != nil)
	select {
	case <-h.Done():
		fmt.Println("closed: true")
	default:
		fmt.Println("closed: false")
	}
	// Output:
	// broken failed plugin_failed
	// activation recorded: true
	// close returned error: true
	// closed: true
}
