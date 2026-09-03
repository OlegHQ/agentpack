package manifest

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/mode"
)

type expressionKind uint8

const (
	expressionKeyValue expressionKind = iota + 1
	expressionTable
)

type documentExpression struct {
	kind  expressionKind
	table []string
	key   []string
	start int
	end   int
}

func assignmentExists(data []byte, fullPath []string) (bool, error) {
	expressions, err := parseDocument(data)
	if err != nil {
		return false, err
	}
	for _, expression := range expressions {
		if expression.kind == expressionKeyValue && equalPath(joinPath(expression.table, expression.key), fullPath) {
			return true, nil
		}
	}
	return false, nil
}

func parseDocument(data []byte) ([]documentExpression, error) {
	parser := unstable.Parser{KeepComments: true}
	parser.Reset(data)
	var expressions []documentExpression
	var table []string
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.Table, unstable.ArrayTable:
			key := nodeKey(node)
			if len(key) == 0 {
				continue
			}
			table = key
			first := node.Child()
			start := lineStart(data, int(first.Raw.Offset)-1)
			expressions = append(expressions, documentExpression{
				kind: expressionTable, table: append([]string(nil), table...),
				start: start, end: lineEnd(data, start),
			})
		case unstable.KeyValue:
			key := nodeKey(node)
			start := lineStart(data, int(node.Raw.Offset))
			expressions = append(expressions, documentExpression{
				kind: expressionKeyValue, table: append([]string(nil), table...), key: key,
				start: start, end: lineEnd(data, int(node.Raw.Offset)+int(node.Raw.Length)),
			})
		}
	}
	if err := parser.Error(); err != nil {
		return nil, fmt.Errorf("parse editable TOML: %w", err)
	}
	return expressions, nil
}

func nodeKey(node *unstable.Node) []string {
	iterator := node.Key()
	var key []string
	for iterator.Next() {
		key = append(key, string(iterator.Node().Data))
	}
	return key
}

func insertAssignment(data []byte, table []string, key string, value string, onlyIfMissing bool) ([]byte, error) {
	expressions, err := parseDocument(data)
	if err != nil {
		return nil, err
	}
	fullKey := append(append([]string(nil), table...), key)
	for _, expression := range expressions {
		if expression.kind == expressionKeyValue && equalPath(joinPath(expression.table, expression.key), fullKey) {
			if onlyIfMissing {
				return data, nil
			}
			line := []byte(quoteKeySegment(key) + " = " + value + "\n")
			return replaceBytes(data, expression.start, expression.end, line), nil
		}
	}
	position := -1
	for _, expression := range expressions {
		if equalPath(expression.table, table) {
			if expression.end > position {
				position = expression.end
			}
		}
	}
	line := []byte(quoteKeySegment(key) + " = " + value + "\n")
	if position >= 0 {
		return replaceBytes(data, position, position, line), nil
	}
	var addition bytes.Buffer
	if len(data) != 0 && data[len(data)-1] != '\n' {
		addition.WriteByte('\n')
	}
	if len(data) != 0 && !bytes.HasSuffix(data, []byte("\n\n")) {
		addition.WriteByte('\n')
	}
	addition.WriteByte('[')
	for index, segment := range table {
		if index != 0 {
			addition.WriteByte('.')
		}
		addition.WriteString(quoteKeySegment(segment))
	}
	addition.WriteString("]\n")
	addition.Write(line)
	return append(append([]byte(nil), data...), addition.Bytes()...), nil
}

func deleteAssignmentOrSection(data []byte, fullPath []string) ([]byte, bool, error) {
	expressions, err := parseDocument(data)
	if err != nil {
		return nil, false, err
	}
	for _, expression := range expressions {
		if expression.kind == expressionKeyValue && equalPath(joinPath(expression.table, expression.key), fullPath) {
			return replaceBytes(data, expression.start, expression.end, nil), true, nil
		}
	}
	for index, expression := range expressions {
		if expression.kind != expressionTable || !equalPath(expression.table, fullPath) {
			continue
		}
		end := len(data)
		for _, next := range expressions[index+1:] {
			if next.kind == expressionTable {
				end = next.start
				break
			}
		}
		return replaceBytes(data, expression.start, end, nil), true, nil
	}
	return data, false, nil
}

func replaceSections(data []byte, prefix []string, replacement []byte) ([]byte, error) {
	expressions, err := parseDocument(data)
	if err != nil {
		return nil, err
	}
	type span struct{ start, end int }
	var spans []span
	for index, expression := range expressions {
		if expression.kind != expressionTable || !hasPathPrefix(expression.table, prefix) {
			continue
		}
		end := len(data)
		for _, next := range expressions[index+1:] {
			if next.kind == expressionTable {
				end = next.start
				break
			}
		}
		if len(spans) != 0 && expression.start <= spans[len(spans)-1].end {
			if end > spans[len(spans)-1].end {
				spans[len(spans)-1].end = end
			}
		} else {
			spans = append(spans, span{expression.start, end})
		}
	}
	result := append([]byte(nil), data...)
	for index := len(spans) - 1; index >= 0; index-- {
		result = replaceBytes(result, spans[index].start, spans[index].end, nil)
	}
	result = bytes.TrimRight(result, "\n")
	if len(replacement) != 0 {
		if len(result) != 0 {
			result = append(result, '\n', '\n')
		}
		result = append(result, replacement...)
	}
	if len(result) != 0 && result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	return result, nil
}

func renderMCPServer(server mcp.Server) string {
	fields := make([]string, 0, 6)
	if server.Type != nil {
		fields = append(fields, "type = "+quoteString(*server.Type))
	}
	if server.Command != nil {
		fields = append(fields, "command = "+quoteString(*server.Command))
	}
	if len(server.Args) != 0 {
		fields = append(fields, "args = "+renderStringArray(server.Args))
	}
	if len(server.Env) != 0 {
		keys := make([]string, 0, len(server.Env))
		for key := range server.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		values := make([]string, 0, len(keys))
		for _, key := range keys {
			values = append(values, quoteKeySegment(key)+" = "+quoteString(server.Env[key]))
		}
		fields = append(fields, "env = { "+strings.Join(values, ", ")+" }")
	}
	if server.URL != nil {
		fields = append(fields, "url = "+quoteString(*server.URL))
	}
	if server.Disabled != nil {
		fields = append(fields, "disabled = "+strconv.FormatBool(*server.Disabled))
	}
	if len(fields) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(fields, ", ") + " }"
}

func renderModes(modes map[string]mode.Definition) []byte {
	names := make([]string, 0, len(modes))
	for name := range modes {
		names = append(names, name)
	}
	sort.Strings(names)
	var output strings.Builder
	for index, name := range names {
		if index != 0 {
			output.WriteByte('\n')
		}
		definition := modes[name]
		definition.ApplyDefaults()
		output.WriteString("[modes.")
		output.WriteString(quoteKeySegment(name))
		output.WriteString("]\nbase = ")
		output.WriteString(quoteString(string(definition.Base)))
		output.WriteByte('\n')
		if len(definition.Enable) != 0 {
			output.WriteString("enable = ")
			output.WriteString(renderStringArray(definition.Enable))
			output.WriteByte('\n')
		}
		if len(definition.Disable) != 0 {
			output.WriteString("disable = ")
			output.WriteString(renderStringArray(definition.Disable))
			output.WriteByte('\n')
		}
	}
	return []byte(output.String())
}

func renderStringArray(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = quoteString(value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func quoteKeySegment(value string) string {
	if value != "" {
		bare := true
		for _, char := range value {
			if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-') {
				bare = false
				break
			}
		}
		if bare {
			return value
		}
	}
	return quoteString(value)
}

func quoteString(value string) string {
	var output strings.Builder
	output.WriteByte('"')
	for len(value) != 0 {
		char, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		switch char {
		case '\b':
			output.WriteString(`\b`)
		case '\t':
			output.WriteString(`\t`)
		case '\n':
			output.WriteString(`\n`)
		case '\f':
			output.WriteString(`\f`)
		case '\r':
			output.WriteString(`\r`)
		case '"':
			output.WriteString(`\"`)
		case '\\':
			output.WriteString(`\\`)
		default:
			if char < 0x20 || char == 0x7f {
				fmt.Fprintf(&output, `\u%04X`, char)
			} else {
				output.WriteRune(char)
			}
		}
	}
	output.WriteByte('"')
	return output.String()
}

func equalPath(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func joinPath(left, right []string) []string {
	result := make([]string, 0, len(left)+len(right))
	result = append(result, left...)
	return append(result, right...)
}

func hasPathPrefix(path, prefix []string) bool {
	return len(path) >= len(prefix) && equalPath(path[:len(prefix)], prefix)
}

func lineStart(data []byte, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(data) {
		offset = len(data)
	}
	if index := bytes.LastIndexByte(data[:offset], '\n'); index >= 0 {
		return index + 1
	}
	return 0
}

func lineEnd(data []byte, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(data) {
		offset = len(data)
	}
	if index := bytes.IndexByte(data[offset:], '\n'); index >= 0 {
		return offset + index + 1
	}
	return len(data)
}

func replaceBytes(data []byte, start, end int, replacement []byte) []byte {
	result := make([]byte, 0, len(data)-(end-start)+len(replacement))
	result = append(result, data[:start]...)
	result = append(result, replacement...)
	result = append(result, data[end:]...)
	return result
}
