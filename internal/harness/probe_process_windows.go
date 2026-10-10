package harness

import "os/exec"

// shortcut: Windows kills the direct child only; require a Job Object before supporting descendant-producing probes.
func configureProbeProcess(cmd *exec.Cmd) {}
