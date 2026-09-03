//go:build windows

package harness

import (
	"fmt"
	"os"
	"os/exec"
)

func linkDirectory(native, staged string) error {
	if err := os.Symlink(native, staged); err == nil {
		return nil
	}
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", staged, native).CombinedOutput(); err != nil {
		return fmt.Errorf("create directory junction %s: %w: %s", staged, err, output)
	}
	return nil
}
