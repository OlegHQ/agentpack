package cli

import (
	"fmt"
	"slices"
	"strings"
)

type Global struct {
	ProjectRoot    string
	DefinitionRoot string
	WorkspaceRoot  string
	Env            string // environment name/path; resolved before binding fallthrough
	Quiet          bool
	NoProgress     bool
	Yolo           bool
	Mode           string
	Debug          bool
	Proxy          bool
	StrictExternal bool
}

type Invocation struct {
	Global  Global
	Command string
	Args    []string
}

// Parse removes global options wherever they appear before --. Remaining
// arguments are deliberately left untouched for command-specific parsing and
// harness passthrough.
func Parse(arguments []string) (Invocation, error) {
	var invocation Invocation
	var rest []string
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			rest = append(rest, arguments[index:]...)
			break
		}
		switch argument {
		case "-q", "--quiet":
			invocation.Global.Quiet = true
		case "--no-progress":
			invocation.Global.NoProgress = true
		case "--yolo":
			invocation.Global.Yolo = true
		case "--debug":
			invocation.Global.Debug = true
		case "--proxy":
			invocation.Global.Proxy = true
		case "--strict-external":
			invocation.Global.StrictExternal = true
		case "--project-root", "--definition-root", "--workspace", "--mode":
			if index+1 >= len(arguments) {
				return Invocation{}, fmt.Errorf("%s requires a value", argument)
			}
			index++
			switch argument {
			case "--project-root":
				invocation.Global.ProjectRoot = arguments[index]
			case "--definition-root":
				invocation.Global.DefinitionRoot = arguments[index]
			case "--workspace":
				invocation.Global.WorkspaceRoot = arguments[index]
			default:
				invocation.Global.Mode = arguments[index]
			}
		case "--env":
			// Environment selector. KEY=VALUE belongs to `mcp add --env` and must pass through.
			if index+1 >= len(arguments) {
				return Invocation{}, fmt.Errorf("%s requires a value", argument)
			}
			if strings.Contains(arguments[index+1], "=") {
				rest = append(rest, argument)
				continue
			}
			index++
			invocation.Global.Env = arguments[index]
		default:
			if value, found := strings.CutPrefix(argument, "--project-root="); found {
				invocation.Global.ProjectRoot = value
			} else if value, found := strings.CutPrefix(argument, "--definition-root="); found {
				invocation.Global.DefinitionRoot = value
			} else if value, found := strings.CutPrefix(argument, "--workspace="); found {
				invocation.Global.WorkspaceRoot = value
			} else if value, found := strings.CutPrefix(argument, "--env="); found {
				if strings.Contains(value, "=") {
					rest = append(rest, argument)
				} else {
					invocation.Global.Env = value
				}
			} else if value, found := strings.CutPrefix(argument, "--mode="); found {
				invocation.Global.Mode = value
			} else {
				rest = append(rest, argument)
			}
		}
	}
	if len(rest) == 0 {
		return Invocation{}, fmt.Errorf("a command is required")
	}
	invocation.Command, invocation.Args = rest[0], rest[1:]
	if invocation.Command == "cursor-agent" {
		invocation.Command = "agent"
	}
	return invocation, nil
}

func takeFlag(arguments []string, name string) (string, []string, bool, error) {
	for index, argument := range arguments {
		if argument == name {
			if index+1 == len(arguments) {
				return "", nil, false, fmt.Errorf("%s requires a value", name)
			}
			return arguments[index+1], slices.Concat(arguments[:index], arguments[index+2:]), true, nil
		}
		if value, found := strings.CutPrefix(argument, name+"="); found {
			return value, slices.Concat(arguments[:index], arguments[index+1:]), true, nil
		}
	}
	return "", arguments, false, nil
}

func takeBool(arguments []string, name string) ([]string, bool) {
	for index, argument := range arguments {
		if argument == name {
			return slices.Concat(arguments[:index], arguments[index+1:]), true
		}
	}
	return arguments, false
}
