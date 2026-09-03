package integration

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseDescriptionIncludesInstallCommandsAndAssets(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile("../.goreleaser.yaml")
	if err != nil {
		t.Fatalf("read GoReleaser configuration: %v", err)
	}

	configuration := string(contents)
	required := []string{
		"## Install agentpack {{ .Version }}",
		"https://github.com/OlegHQ/agentpack/releases/download/{{ .Tag }}/agentpack-installer.sh | sh",
		`powershell -ExecutionPolicy ByPass -c "irm https://github.com/OlegHQ/agentpack/releases/download/{{ .Tag }}/agentpack-installer.ps1 | iex"`,
		"glob: ./scripts/agentpack-installer.sh",
		"glob: ./scripts/agentpack-installer.ps1",
		"name_template: checksums.txt",
		"write only to `$HOME/.local/bin`",
		"do not modify shell profiles or `PATH`",
	}
	for _, expected := range required {
		if !strings.Contains(configuration, expected) {
			t.Errorf("GoReleaser configuration is missing release requirement %q", expected)
		}
	}
}
