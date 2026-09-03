package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const NoAttributionBody = `# Attribution policy

Do not add any AI-attribution lines to git commits, pull requests, or other artifacts you author.
Specifically, do not include:

- ` + "`Co-Authored-By: <model> <noreply@...>`" + ` trailers.
- ` + "`Generated with [agent name]`" + ` footers, banners, or similar credit lines.
- Tool/agent name signatures in commit messages or PR descriptions.

Write commit messages and PR descriptions as if a human author wrote them.
`

func CopySelectedEntries(sourceRoot, destinationRoot string, entries []string) error {
	for _, entry := range entries {
		source := filepath.Join(sourceRoot, entry)
		if _, err := os.Lstat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := copyTree(source, filepath.Join(destinationRoot, entry)); err != nil {
			return err
		}
	}
	return nil
}

func copyTree(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, info.Mode().Perm())
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := copyTree(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

const guidanceBegin = "<!-- agentpack:guidance:begin -->"
const guidanceEnd = "<!-- agentpack:guidance:end -->"

func WriteGuidance(path, blob string) error {
	existing, _ := os.ReadFile(path)
	base := stripGuidance(string(existing))
	output := strings.TrimRight(base, "\n")
	if output == "" {
		output = "# AGENTS.md"
	}
	output += "\n\n" + guidanceBegin + "\n" + strings.TrimSpace(blob) + "\n" + guidanceEnd + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		return fmt.Errorf("write guidance %s: %w", path, err)
	}
	return nil
}

func stripGuidance(text string) string {
	begin := strings.Index(text, guidanceBegin)
	if begin < 0 {
		return text
	}
	relativeEnd := strings.Index(text[begin:], guidanceEnd)
	if relativeEnd < 0 {
		return text
	}
	end := begin + relativeEnd + len(guidanceEnd)
	return strings.TrimRight(text[:begin], "\n") + strings.TrimLeft(text[end:], "\n")
}
