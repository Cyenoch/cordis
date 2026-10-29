package processtree

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// Run owns the command and its descendants, including inherited output pipes.
// The command must not already have been started.
func Run(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	tree, err := Start(cmd)
	if err != nil {
		return err
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	select {
	case err = <-finished:
	case <-ctx.Done():
		killErr := tree.Kill()
		err = errors.Join(ctx.Err(), killErr, <-finished)
	}
	return errors.Join(err, tree.Kill(), tree.Close())
}
