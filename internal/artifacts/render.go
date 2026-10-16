package artifacts

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/olegpustovit/agentpack/internal/harness"
	"github.com/pelletier/go-toml/v2"
)

type yamlField struct {
	key   string
	value any
}

var (
	commandExtraKeys  = []string{"agent", "allowed-tools", "context", "disable-model-invocation", "model", "subtask"}
	claudeCommandKeys = []string{"agent", "allowed-tools", "argument-hint", "arguments", "context", "disable-model-invocation", "model", "subtask"}
	cursorCommandKeys = []string{"agent", "allowed-tools", "context", "disable-model-invocation", "model", "permission", "subtask"}
	skillExtraKeys    = []string{"allowed-tools", "agent", "compatibility", "context", "disallowedTools", "license", "mcpServers", "metadata", "mode", "model", "permission", "subtask", "tools"}
	agentExtraKeys    = []string{"color", "disallowedTools", "hidden", "hooks", "mcpServers", "mode", "model", "permission", "subtask", "tools"}
)

func (artifact Markdown) Render(target harness.Target) Rendered {
	kind := RenderedKind(target, artifact.Kind)
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

// RenderedKind is the kind an artifact takes on a target: Codex has skills
// only, and rules become skills where the target has no rule files.
func RenderedKind(target harness.Target, source Kind) Kind {
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
	var warnings []string
	if target == harness.Claude || target == harness.Grok {
		fields = []yamlField{{"description", artifact.Description}, {"name", artifact.Name}}
	} else if target == harness.Cursor {
		fields = []yamlField{{"name", artifact.Name}, {"description", artifact.Description}}
	} else {
		fields = []yamlField{{"description", artifact.Description}}
	}
	if target != harness.Agy {
		allowed := commandExtraKeys
		switch target {
		case harness.Cursor:
			allowed = cursorCommandKeys
		case harness.Claude, harness.Grok:
			allowed = claudeCommandKeys
		}
		extras := artifact.ExtraFrontmatter
		if model, found := extras["model"]; found && target == harness.OpenCode {
			if text, _ := model.(string); !strings.Contains(text, "/") {
				// A Claude Code alias such as sonnet names no OpenCode model.
				extras = make(map[string]any, len(artifact.ExtraFrontmatter))
				for key, value := range artifact.ExtraFrontmatter {
					if key != "model" {
						extras[key] = value
					}
				}
				warnings = append(warnings, fmt.Sprintf("OpenCode command %s: model %v is not an OpenCode provider/model id; the command uses the session's model", artifact.Name, model))
			}
		}
		fields = mergeExtra(fields, extras, allowed)
	}
	return Rendered{RelativePath: "commands/" + artifact.tailPath, Contents: renderMarkdown(fields, artifact.Body), Warnings: warnings}
}

func (artifact Markdown) renderAgent(target harness.Target) Rendered {
	fields := []yamlField{{"name", artifact.Name}, {"description", artifact.Description}}
	var warnings []string
	if target == harness.OpenCode {
		extras, notes := openCodeAgentExtras(artifact.ExtraFrontmatter)
		fields = mergeExtra(fields, extras, agentExtraKeys)
		for _, note := range notes {
			warnings = append(warnings, "OpenCode agent "+artifact.Name+": "+note)
		}
	} else if target != harness.Agy {
		fields = mergeExtra(fields, artifact.ExtraFrontmatter, agentExtraKeys)
	}
	return Rendered{RelativePath: artifact.agentPath(target), Contents: renderMarkdown(fields, artifact.Body), Warnings: warnings}
}

// agentPath is where an agent is staged for target. OpenCode finds agents in
// subdirectories of agents/; Claude Code, Grok, Cursor and Antigravity read only
// the top level, so an agent a package keeps in a subdirectory is staged there.
func (artifact Markdown) agentPath(target harness.Target) string {
	if target == harness.OpenCode || !strings.Contains(artifact.tailPath, "/") {
		return "agents/" + artifact.tailPath
	}
	return "agents/" + path.Base(artifact.tailPath)
}

// NestedPath is where the artifact sits in its package's own layout, under its
// kind's directory (agents/team/reviewer.md).
func (artifact Markdown) NestedPath() string {
	return artifact.Kind.Word() + "s/" + artifact.tailPath
}

// openCodeTools maps Claude Code tool names to the OpenCode tools they
// correspond to. Every OpenCode tool listed here is switched off for an agent
// whose Claude Code tools list does not grant it.
var openCodeTools = map[string]string{
	"read": "read", "write": "write", "edit": "edit", "multiedit": "edit", "notebookedit": "edit",
	"bash": "bash", "grep": "grep", "glob": "glob", "ls": "list", "list": "list",
	"webfetch": "webfetch", "websearch": "websearch", "task": "task", "agent": "task",
	"todowrite": "todowrite", "skill": "skill",
}

// openCodeColors gives Claude Code's named agent colors the hex form OpenCode
// requires; OpenCode's own theme color names pass through.
var openCodeColors = map[string]string{
	"red": "#ef4444", "blue": "#3b82f6", "green": "#22c55e", "yellow": "#eab308",
	"purple": "#a855f7", "orange": "#f97316", "pink": "#ec4899", "cyan": "#06b6d4",
	"primary": "primary", "secondary": "secondary", "accent": "accent", "success": "success",
	"warning": "warning", "error": "error", "info": "info",
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// openCodeAgentExtras rewrites the frontmatter OpenCode rejects: it validates
// every agent file and refuses to load its whole configuration when one field
// has another form. Claude Code writes an agent's tools as a comma-separated
// string or a list of allowed tools, where OpenCode documents an object of
// tool name to boolean; a color is a name, where OpenCode wants hex; a model
// is an alias such as sonnet, where OpenCode wants provider/model. It returns
// a note for each value that could not be carried over.
func openCodeAgentExtras(source map[string]any) (map[string]any, []string) {
	extras := make(map[string]any, len(source))
	for key, value := range source {
		extras[key] = value
	}
	var notes []string
	if value, found := source["color"]; found {
		text, _ := value.(string)
		if mapped, known := openCodeColors[strings.ToLower(text)]; known {
			extras["color"] = mapped
		} else if !hexColor.MatchString(text) {
			delete(extras, "color")
			notes = append(notes, fmt.Sprintf("color %v is not a color OpenCode accepts and is left out", value))
		}
	}
	if value, found := source["mode"]; found {
		if text, _ := value.(string); text != "subagent" && text != "primary" && text != "all" {
			delete(extras, "mode")
			notes = append(notes, fmt.Sprintf("mode %v is not an OpenCode agent mode and is left out", value))
		}
	}
	if value, found := source["model"]; found {
		if text, _ := value.(string); !strings.Contains(text, "/") {
			delete(extras, "model")
			notes = append(notes, fmt.Sprintf("model %v is not an OpenCode provider/model id; the agent uses the session's model", value))
		}
	}
	allowed, hasAllowed := claudeToolList(source["tools"])
	denied, hasDenied := claudeToolList(source["disallowedTools"])
	delete(extras, "disallowedTools")
	if _, native := source["tools"].(map[string]any); native || !hasAllowed && !hasDenied {
		if !native {
			delete(extras, "tools")
		}
		return extras, notes
	}
	tools := make(map[string]any)
	var dropped []string
	enable := func(names []string, value bool) {
		for _, name := range names {
			mapped, found := openCodeTools[strings.ToLower(name)]
			if !found {
				dropped = append(dropped, name)
				continue
			}
			tools[mapped] = value
		}
	}
	if hasAllowed {
		for _, mapped := range openCodeTools {
			tools[mapped] = false
		}
		enable(allowed, true)
	}
	enable(denied, false)
	extras["tools"] = tools
	if len(dropped) != 0 {
		sort.Strings(dropped)
		notes = append(notes, fmt.Sprintf("tool(s) %s have no OpenCode equivalent and are left out of its tools map", strings.Join(dropped, ", ")))
	}
	return extras, notes
}

// claudeToolList reads `tools: Read, Grep, Bash(git:*)` or the YAML list form
// and returns the bare tool names.
func claudeToolList(value any) ([]string, bool) {
	var items []string
	switch typed := value.(type) {
	case string:
		depth, start := 0, 0
		for index, character := range typed {
			switch character {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 0 {
					items = append(items, typed[start:index])
					start = index + 1
				}
			}
		}
		items = append(items, typed[start:])
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				items = append(items, text)
			}
		}
	case []string:
		items = typed
	default:
		return nil, false
	}
	var names []string
	for _, item := range items {
		name, _, _ := strings.Cut(strings.TrimSpace(item), "(")
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names, true
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

// CodexAgent renders a subagent as a Codex custom agent file,
// agents/<name>.toml with name, description and developer_instructions, the
// form Codex reads from $CODEX_HOME/agents and .codex/agents. The agent's
// file name is its Codex name, so that it is a plain identifier. It reports
// false for anything that is not a subagent.
func (artifact Markdown) CodexAgent() (Rendered, bool) {
	if artifact.Kind != Agent {
		return Rendered{}, false
	}
	name := strings.TrimSuffix(filepath.Base(artifact.tailPath), filepath.Ext(artifact.tailPath))
	encoded, err := toml.Marshal(map[string]any{
		"name":                   name,
		"description":            artifact.Description,
		"developer_instructions": strings.TrimSpace(artifact.Body) + "\n",
	})
	if err != nil {
		return Rendered{}, false
	}
	return Rendered{RelativePath: "agents/" + name + ".toml", Contents: string(encoded)}, true
}
