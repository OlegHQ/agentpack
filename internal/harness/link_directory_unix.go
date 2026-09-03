//go:build !windows

package harness

import "os"

func linkDirectory(native, staged string) error { return os.Symlink(native, staged) }
