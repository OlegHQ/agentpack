package artifacts

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/OlegHQ/agentpack/internal/harness"
	"github.com/goccy/go-yaml"
)

type yamlField struct {
	key   string
	value any
}

var (
	commandExtraKeys  = []string{"agent", "allowed-tools", "context", "disable-model-invocation", "model", "subtask"}
	cursorCommandKeys = []string{"agent", "allowed-tools", "context", "disable-model-invocation", "model", "permission", "subtask"}
	skillExtraKeys    = []string{"allowed-tools", "agent", "compatibility", "context", "disallowedTools", "license", "mcpServers", "metadata", "mode", "model", "permission", "subtask", "tools"}
	agentExtraKeys    = []string{"color", "disallowedTools", "hidden", "hooks", "mcpServers", "mode", "model", "permission", "subtask", "tools"}
)

func (artifact Markdown) Render(target harness.Target) Rendered {
	kind := renderedKind(target, artifact.Kind)
	switch kind {
	case Skill:
		return artifact.renderSkill(target)
	case Command:
		return artifact.renderCommand(target)
	case Agent:
		return artifact.renderAgent(target)
	default:
		return artifact.renderRule()
	}
}

func renderedKind(target harness.Target, source Kind) Kind {
	if target == harness.Codex {
		return Skill
	}
	if source == Rule && target != harness.Cursor && target != harness.Agy {
		return Skill
	}
	return source
}

func (artifact Markdown) renderSkill(target harness.Target) Rendered {
	fields := []yamlField{{"name", artifact.Name}, {"description", artifact.Description}}
	if artifact.DisableModelInvocation || artifact.Kind == Command {
		fields = append(fields, yamlField{"disable-model-invocation", true})
	}
	if target != harness.Agy {
		fields = mergeExtra(fields, artifact.ExtraFrontmatter, skillExtraKeys)
	}
	return Rendered{RelativePath: filepath.ToSlash(filepath.Join("skills", artifact.StorageName, "SKILL.md")), Contents: renderMarkdown(fields, artifact.skillBody(target))}
}

func (artifact Markdown) renderCommand(target harness.Target) Rendered {
	var fields []yamlField
	if target == harness.Claude || target == harness.Grok {
		fields = []yamlField{{"description", artifact.Description}, {"name", artifact.Name}}
	} else if target == harness.Cursor {
		fields = []yamlField{{"name", artifact.Name}, {"description", artifact.Description}}
	} else {
		fields = []yamlField{{"description", artifact.Description}}
	}
	if target != harness.Agy {
		allowed := commandExtraKeys
		if target == harness.Cursor {
			allowed = cursorCommandKeys
		}
		fields = mergeExtra(fields, artifact.ExtraFrontmatter, allowed)
	}
	return Rendered{RelativePath: "commands/" + artifact.tailPath, Contents: renderMarkdown(fields, artifact.Body)}
}

func (artifact Markdown) renderAgent(target harness.Target) Rendered {
	fields := []yamlField{{"name", artifact.Name}, {"description", artifact.Description}}
	if target != harness.Agy {
		fields = mergeExtra(fields, artifact.ExtraFrontmatter, agentExtraKeys)
	}
	return Rendered{RelativePath: "agents/" + artifact.tailPath, Contents: renderMarkdown(fields, artifact.Body)}
}

func (artifact Markdown) renderRule() Rendered {
	fields := []yamlField{{"description", artifact.Description}}
	if len(artifact.Globs) == 1 {
		fields = append(fields, yamlField{"globs", artifact.Globs[0]})
	} else if len(artifact.Globs) > 1 {
		fields = append(fields, yamlField{"globs", artifact.Globs})
	}
	if artifact.AlwaysApply {
		fields = append(fields, yamlField{"alwaysApply", true})
	}
	return Rendered{RelativePath: "rules/" + artifact.tailPath, Contents: renderMarkdown(fields, artifact.Body)}
}

func (artifact Markdown) skillBody(target harness.Target) string {
	if artifact.Kind != Rule || target == harness.Cursor || len(artifact.Globs) == 0 && !artifact.AlwaysApply {
		return artifact.Body
	}
	var output strings.Builder
	output.WriteString("## Original rule scope\n")
	if artifact.AlwaysApply {
		output.WriteString("- Applies in every session.\n")
	}
	if len(artifact.Globs) != 0 {
		quoted := make([]string, len(artifact.Globs))
		for index, glob := range artifact.Globs {
			quoted[index] = "`" + glob + "`"
		}
		output.WriteString("- Original Cursor globs: " + strings.Join(quoted, ", ") + "\n")
	}
	output.WriteByte('\n')
	output.WriteString(strings.TrimLeft(artifact.Body, "\n"))
	return trailingNewline(output.String())
}

func mergeExtra(fields []yamlField, extras map[string]any, allowed []string) []yamlField {
	existing := make(map[string]bool, len(fields))
	for _, field := range fields {
		existing[field.key] = true
	}
	for _, key := range allowed {
		if value, found := extras[key]; found && !existing[key] {
			fields = append(fields, yamlField{key, value})
		}
	}
	return fields
}

func renderMarkdown(fields []yamlField, body string) string {
	var output strings.Builder
	output.WriteString("---\n")
	for _, field := range fields {
		encoded, err := yaml.Marshal(map[string]any{field.key: field.value})
		if err != nil {
			panic(fmt.Sprintf("frontmatter serialization failed: %v", err))
		}
		output.Write(encoded)
	}
	output.WriteString("---\n\n")
	output.WriteString(strings.TrimLeft(body, "\n"))
	return trailingNewline(output.String())
}
