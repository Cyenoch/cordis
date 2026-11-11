package main

import "context"

// The output below is quoted in docs/kernel.md.
func Example_run() {
	if err := run(context.Background()); err != nil {
		panic(err)
	}
	// Output:
	// shout: waiting until a greeter exists
	// shout: started with hello, plugin
	// HELLO, WORLD!
	// greeter: stopped hello
	// shout: started with goodbye, plugin
	// GOODBYE, WORLD!
	// rejected: greeting must not be empty
	// greeter: stopped goodbye
}
