package processtree

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The test binary plays three parts, chosen by this variable: the test, a
// host with a console of its own, and the processes that host starts.
const consoleRole = "CORDIS_CONSOLE_ROLE"

func TestMain(m *testing.M) {
	switch os.Getenv(consoleRole) {
	case "host":
		os.Exit(consoleHost())
	case "child":
		os.Exit(consoleChild())
	}
	os.Exit(m.Run())
}

// Ctrl+C in the host's console reaches every process attached to it. A
// plugin started through Start is not interrupted by it: the host stops its
// plugins in order. A process started plainly in the same console is
// interrupted, which shows the keystroke was really delivered.
func TestConsoleInterruptDoesNotReachTheTree(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// A console of its own: the Ctrl+C generated there must not reach the
	// processes that run this test.
	host := exec.Command(self)
	host.Env = append(os.Environ(), consoleRole+"=host")
	host.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	output, err := host.CombinedOutput()
	if err != nil {
		t.Fatalf("host: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "plain: interrupted\ntree: undisturbed" {
		t.Fatalf("after Ctrl+C in the host's console:\n%s", got)
	}
}

// consoleHost starts one child plainly and one through Start, presses Ctrl+C
// in its console and reports what each child saw.
func consoleHost() int {
	// The host takes its own Ctrl+C and carries on, as one that stops its
	// plugins in order does.
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	// Whether Ctrl+C is ignored is inherited, and whoever started the tests
	// may have turned it off. Turn it back on here, so that the children
	// start from what a process launched from a terminal has.
	if result, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler").Call(0, 0); result == 0 {
		fmt.Println("enable Ctrl+C:", err)
		return 1
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Println(err)
		return 1
	}
	type child struct {
		name  string
		lines *bufio.Reader
		stdin io.WriteCloser
	}
	start := func(name string, launch func(*exec.Cmd) error) (*child, error) {
		command := exec.Command(self)
		command.Env = append(os.Environ(), consoleRole+"=child")
		stdin, err := command.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := command.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := launch(command); err != nil {
			return nil, err
		}
		started := &child{name: name, lines: bufio.NewReader(stdout), stdin: stdin}
		if line, err := started.lines.ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
			return nil, fmt.Errorf("%s did not get ready: %q, %v", name, line, err)
		}
		return started, nil
	}
	plain, err := start("plain", func(command *exec.Cmd) error { return command.Start() })
	if err != nil {
		fmt.Println(err)
		return 1
	}
	var tree *Tree
	inTree, err := start("tree", func(command *exec.Cmd) (err error) {
		tree, err = Start(command)
		return err
	})
	if err != nil {
		fmt.Println(err)
		return 1
	}
	defer tree.Kill()
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0); err != nil {
		fmt.Println("generate Ctrl+C:", err)
		return 1
	}
	// The plain child reports the interrupt by itself. Once it has, the
	// keystroke has been delivered; a moment later the other child is asked
	// what it saw.
	seen, err := plain.lines.ReadString('\n')
	if err != nil {
		fmt.Println("plain child:", err)
		return 1
	}
	fmt.Println("plain:", strings.TrimSpace(seen))
	time.Sleep(500 * time.Millisecond)
	inTree.stdin.Close()
	seen, err = inTree.lines.ReadString('\n')
	if err != nil {
		fmt.Println("tree child:", err)
		return 1
	}
	fmt.Println("tree:", strings.TrimSpace(seen))
	return 0
}

// consoleChild says whether it was interrupted before its input closed.
func consoleChild() int {
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	closed := make(chan struct{})
	go func() {
		io.Copy(io.Discard, os.Stdin)
		close(closed)
	}()
	fmt.Println("ready")
	select {
	case <-interrupted:
		fmt.Println("interrupted")
	case <-closed:
		fmt.Println("undisturbed")
	case <-time.After(time.Minute):
		fmt.Println("nothing happened")
	}
	return 0
}
