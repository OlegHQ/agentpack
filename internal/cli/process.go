package cli

import (
	"errors"
	"os/exec"
)

func runProcess(command *exec.Cmd) (int, error) {
	err := command.Run()
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return 1, err
}
