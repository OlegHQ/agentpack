package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/hooks"
)

func (runner Runner) runHook(ctx context.Context, arguments []string) (int, error) {
	if len(arguments) == 0 {
		return 2, fmt.Errorf("hook-exec requires a kind")
	}
	kind, args := arguments[0], arguments[1:]
	targetText, args, found, err := takeFlag(args, "--target")
	if err != nil {
		return 2, err
	}
	if !found {
		return 2, fmt.Errorf("hook-exec requires --target")
	}
	target, err := base.ParseTarget(targetText)
	if err != nil {
		return 2, err
	}
	stdin, err := io.ReadAll(runner.Stdin)
	if err != nil {
		return 1, err
	}
	if kind == "dispatch" {
		eventText, args, found, err := takeFlag(args, "--event")
		if err != nil || !found {
			return 2, flagError(err, "--event")
		}
		directory, args, found, err := takeFlag(args, "--specs-dir")
		if err != nil || !found {
			return 2, flagError(err, "--specs-dir")
		}
		if err := noArgs(args); err != nil {
			return 2, err
		}
		event, ok := hooks.ParseEvent(eventText)
		if !ok {
			return 2, fmt.Errorf("unknown hook event %q", eventText)
		}
		outcome, err := hooks.Dispatch(ctx, hooks.DispatchArgs{Target: target, Event: event, SpecsDirectory: directory, Stdin: stdin})
		if err != nil {
			return 1, err
		}
		if err := writeJSON(runner.Stdout, outcome.JSON); err != nil {
			return 1, err
		}
		return outcome.ExitCode, nil
	}
	if kind == "inject-guidance" {
		eventText, args, found, err := takeFlag(args, "--event")
		if err != nil || !found {
			return 2, flagError(err, "--event")
		}
		file, args, found, err := takeFlag(args, "--file")
		if err != nil || !found {
			return 2, flagError(err, "--file")
		}
		if err := noArgs(args); err != nil {
			return 2, err
		}
		body, err := os.ReadFile(file)
		if err != nil {
			return 1, err
		}
		event, ok := hooks.ParseEvent(eventText)
		if !ok {
			return 2, fmt.Errorf("unknown hook event %q", eventText)
		}
		return 0, writeJSON(runner.Stdout, guidanceOutput(target, event, string(body)))
	}
	specPath, args, found, err := takeFlag(args, "--spec")
	if err != nil || !found {
		return 2, flagError(err, "--spec")
	}
	if err := noArgs(args); err != nil {
		return 2, err
	}
	spec, err := hooks.LoadExecutionSpec(specPath)
	if err != nil {
		return 1, err
	}
	if kind == "command" {
		output, err := hooks.RunCommand(ctx, spec, stdin)
		if err != nil {
			return 1, err
		}
		_, _ = runner.Stdout.Write(output.Stdout)
		_, _ = runner.Stderr.Write(output.Stderr)
		return output.ExitCode, nil
	}
	var result hooks.Result
	switch kind {
	case "http":
		result, err = hooks.RunHTTP(ctx, http.DefaultClient, spec, stdin)
	case "prompt":
		result, err = hooks.RunPrompt(ctx, spec, stdin)
	case "agent":
		result, err = hooks.RunAgent(ctx, spec, stdin)
	default:
		return 2, fmt.Errorf("unknown hook-exec kind %q", kind)
	}
	if err != nil {
		return 1, err
	}
	return 0, writeJSON(runner.Stdout, hooks.HookOutput(target, spec.Event, result))
}

func flagError(err error, name string) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("hook-exec requires %s", name)
}
func guidanceOutput(target base.Target, event hooks.Event, body string) any {
	switch target {
	case base.Claude:
		return hooks.GuidanceHookSpecific(body, event)
	case base.Cursor, base.OpenCode:
		return hooks.GuidanceAdditionalContextContinue(body)
	default:
		return hooks.GuidanceAdditionalContext(body)
	}
}
