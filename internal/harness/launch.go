package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ResolveBinary(environment, fallback string) (string, error) {
	program := strings.TrimSpace(os.Getenv(environment))
	if program == "" {
		program = fallback
	}
	if filepath.IsAbs(program) || strings.ContainsAny(program, "/\\") {
		info, err := os.Stat(program)
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("could not find %q", program)
		}
		return program, nil
	}
	path, err := exec.LookPath(program)
	if err != nil {
		return "", fmt.Errorf("could not find %q: %w", program, err)
	}
	return path, nil
}
func HasFlagValue(arguments []string, flag string) bool {
	prefix := flag + "="
	for index, argument := range arguments {
		if strings.HasPrefix(argument, prefix) || (argument == flag && index+1 < len(arguments)) {
			return true
		}
	}
	return false
}
func HasAny(arguments []string, values ...string) bool {
	for _, argument := range arguments {
		for _, value := range values {
			if argument == value {
				return true
			}
		}
	}
	return false
}
func WorkspaceRoot(projectRoot string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return projectRoot
	}
	if canonical, err := filepath.EvalSymlinks(cwd); err == nil {
		return canonical
	}
	return cwd
}
func PrependOnce(arguments []string, flag string, aliases ...string) []string {
	if HasAny(arguments, append([]string{flag}, aliases...)...) {
		return arguments
	}
	return append([]string{flag}, arguments...)
}
