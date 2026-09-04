package artifacts

import (
	"path/filepath"
	"strings"
)

type Kind uint8

const (
	Skill Kind = iota
	Command
	Agent
	Rule
)

type SourceVariant uint8

const (
	SkillFrontmatter SourceVariant = iota
	CommandFrontmatter
	CursorPlainCommand
	AgentFrontmatter
	CursorRule
)

type Rendered struct {
	RelativePath string
	Contents     string
}

type Markdown struct {
	Kind                   Kind
	SourceVariant          SourceVariant
	Name                   string
	StorageName            string
	Description            string
	Body                   string
	DisableModelInvocation bool
	AlwaysApply            bool
	Globs                  []string
	ExtraFrontmatter       map[string]any
	tailPath               string
}

func Parse(relativePath, contents, bareSkillName string) (*Markdown, error) {
	relativePath = filepath.ToSlash(filepath.Clean(relativePath))
	if bareSkillName != "" {
		if relativePath != "SKILL.md" {
			return nil, nil
		}
		return parseSkill(bareSkillName, contents, "SKILL.md")
	}
	stripped := stripHarnessPrefix(relativePath)
	parts := strings.Split(stripped, "/")
	if len(parts) == 3 && parts[0] == "skills" && parts[2] == "SKILL.md" {
		return parseSkill(parts[1], contents, "SKILL.md")
	}
	if len(parts) < 2 || !markdownExtension(parts[len(parts)-1]) {
		return nil, nil
	}
	tail := strings.Join(parts[1:], "/")
	switch parts[0] {
	case "commands":
		return parseNamed(Command, contents, tail)
	case "agents":
		return parseNamed(Agent, contents, tail)
	case "rules":
		return parseNamed(Rule, contents, tail)
	default:
		return nil, nil
	}
}

func StagedSkillSupportPath(relativePath, bareSkillName string) (string, bool) {
	relativePath = filepath.ToSlash(filepath.Clean(relativePath))
	if bareSkillName != "" {
		if relativePath == "SKILL.md" {
			return "", false
		}
		return "skills/" + bareSkillName + "/" + relativePath, true
	}
	parts := strings.Split(stripHarnessPrefix(relativePath), "/")
	if len(parts) < 3 || parts[0] != "skills" || strings.Join(parts[2:], "/") == "SKILL.md" {
		return "", false
	}
	return strings.Join(parts, "/"), true
}

func parseSkill(storageName, contents, tail string) (*Markdown, error) {
	frontmatter, body, err := splitFrontmatter(contents)
	if err != nil {
		return nil, err
	}
	common := takeCommon(frontmatter)
	name := common.name
	if name == "" {
		name = storageName
	}
	return &Markdown{Kind: Skill, SourceVariant: SkillFrontmatter, Name: name, StorageName: storageName, Description: descriptionOrInfer(common.description, body, storageName, Skill), Body: body, DisableModelInvocation: common.disableModelInvocation, ExtraFrontmatter: frontmatter, tailPath: tail}, nil
}

func parseNamed(kind Kind, contents, tail string) (*Markdown, error) {
	frontmatter, body, err := splitFrontmatter(contents)
	if err != nil {
		return nil, err
	}
	variant := AgentFrontmatter
	if kind == Command {
		if frontmatter == nil {
			variant = CursorPlainCommand
		} else {
			variant = CommandFrontmatter
		}
	} else if kind == Rule {
		variant = CursorRule
	}
	if frontmatter == nil {
		frontmatter = make(map[string]any)
	}
	common := takeCommon(frontmatter)
	name := strings.TrimSuffix(filepath.Base(tail), filepath.Ext(tail))
	storageName := name
	if common.name != "" {
		name = common.name
	}
	return &Markdown{Kind: kind, SourceVariant: variant, Name: name, StorageName: storageName, Description: descriptionOrInfer(common.description, body, storageName, kind), Body: body, DisableModelInvocation: kind == Command || common.disableModelInvocation, AlwaysApply: common.alwaysApply, Globs: common.globs, ExtraFrontmatter: frontmatter, tailPath: tail}, nil
}

func descriptionOrInfer(description, body, name string, kind Kind) string {
	if description != "" {
		return description
	}
	for _, line := range strings.Split(body, "\n") {
		candidate := strings.TrimSpace(line)
		if candidate == "" {
			continue
		}
		candidate = strings.TrimSpace(strings.TrimLeft(candidate, "#"))
		candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "- "))
		if candidate != "" {
			return truncate(candidate, 160)
		}
	}
	switch kind {
	case Command:
		return "Run " + name
	case Agent:
		return "Use the " + name + " agent"
	case Rule:
		return "Apply the " + name + " rule"
	default:
		return "Use the " + name + " skill"
	}
}

func stripHarnessPrefix(path string) string {
	for _, prefix := range []string{".claude/", ".cursor/", ".opencode/", ".agents/", ".grok/"} {
		if strings.HasPrefix(path, prefix) {
			return strings.TrimPrefix(path, prefix)
		}
	}
	return path
}

func markdownExtension(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".md" || extension == ".mdc"
}

func truncate(value string, length int) string {
	runes := []rune(value)
	if len(runes) <= length {
		return value
	}
	return string(runes[:length])
}
