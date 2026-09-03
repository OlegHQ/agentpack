package artifacts

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

type commonFrontmatter struct {
	name, description      string
	disableModelInvocation bool
	globs                  []string
	alwaysApply            bool
}

func splitFrontmatter(contents string) (map[string]any, string, error) {
	contents = strings.TrimPrefix(contents, "\ufeff")
	after := ""
	switch {
	case strings.HasPrefix(contents, "---\n"):
		after = contents[4:]
	case strings.HasPrefix(contents, "---\r\n"):
		after = contents[5:]
	default:
		return nil, trailingNewline(contents), nil
	}
	offset := 0
	for _, line := range strings.Split(after, "\n") {
		if strings.TrimSuffix(line, "\r") == "---" {
			yamlText := normalizeGlobYAML(after[:offset])
			bodyStart := offset + len(line) + 1
			body := ""
			if bodyStart <= len(after) {
				body = after[bodyStart:]
			}
			frontmatter := make(map[string]any)
			if err := yaml.Unmarshal([]byte(yamlText), &frontmatter); err != nil {
				return nil, "", fmt.Errorf("invalid YAML frontmatter: %w", err)
			}
			return frontmatter, trailingNewline(strings.TrimLeft(body, "\n")), nil
		}
		offset += len(line) + 1
	}
	return nil, trailingNewline(contents), nil
}

func takeCommon(frontmatter map[string]any) commonFrontmatter {
	common := commonFrontmatter{}
	common.name = takeString(frontmatter, "name")
	common.description = takeString(frontmatter, "description")
	common.disableModelInvocation = takeBool(frontmatter, "disable-model-invocation")
	common.alwaysApply = takeBool(frontmatter, "alwaysApply")
	if value, found := frontmatter["globs"]; found {
		delete(frontmatter, "globs")
		switch typed := value.(type) {
		case string:
			common.globs = []string{typed}
		case []any:
			for _, item := range typed {
				if glob, ok := item.(string); ok {
					common.globs = append(common.globs, glob)
				}
			}
		case []string:
			common.globs = append(common.globs, typed...)
		}
	}
	return common
}

func takeString(values map[string]any, key string) string {
	value, found := values[key]
	if !found {
		return ""
	}
	delete(values, key)
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return fmt.Sprint(typed)
	case int, int64, uint64, float64:
		return fmt.Sprint(typed)
	default:
		return ""
	}
}

func takeBool(values map[string]any, key string) bool {
	value, found := values[key]
	if found {
		delete(values, key)
	}
	result, _ := value.(bool)
	return result
}

func normalizeGlobYAML(input string) string {
	var output strings.Builder
	for _, line := range strings.Split(input, "\n") {
		trimmed := strings.TrimSpace(line)
		if value, found := strings.CutPrefix(trimmed, "globs:"); found {
			value = strings.TrimSpace(value)
			if value != "" && !strings.HasPrefix(value, `"`) && !strings.HasPrefix(value, `'`) && !strings.HasPrefix(value, "[") {
				output.WriteString(`globs: "` + strings.ReplaceAll(value, `"`, `\"`) + `"` + "\n")
				continue
			}
		}
		output.WriteString(line)
		output.WriteByte('\n')
	}
	return output.String()
}

func trailingNewline(value string) string {
	if strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}
