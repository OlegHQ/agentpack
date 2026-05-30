use std::process;

use crate::hooks::ir::{ClaudeEvent, NormalizedHookResult};
use crate::hooks::runtime::bridge::{
    forward_process_output, load_spec, read_stdin_bytes, write_json_stdout, HookExecutionSpec,
};
use crate::hooks::runtime::dispatch::{dispatch, DispatchArgs};
use crate::hooks::runtime::handlers;

use super::{
    HookDispatchArgs, HookExecArgs, HookExecKind, HookExecSpecArgs, HookInjectGuidanceArgs,
};

pub fn run(args: HookExecArgs) -> anyhow::Result<()> {
    let stdin_bytes = read_stdin_bytes()?;
    match args.kind {
        HookExecKind::Command(spec_args) => run_command(spec_args, &stdin_bytes),
        HookExecKind::Http(spec_args) => run_json(JsonKind::Http, spec_args, &stdin_bytes),
        HookExecKind::Prompt(spec_args) => run_json(JsonKind::Prompt, spec_args, &stdin_bytes),
        HookExecKind::Agent(spec_args) => run_json(JsonKind::Agent, spec_args, &stdin_bytes),
        HookExecKind::Dispatch(d) => run_dispatch(d, &stdin_bytes),
        HookExecKind::InjectGuidance(args) => run_inject_guidance(args),
    }
}

fn run_command(args: HookExecSpecArgs, stdin_bytes: &[u8]) -> anyhow::Result<()> {
    let spec = load_spec(&args.spec)?;
    let result = handlers::run_command(&spec, stdin_bytes)?;
    forward_process_output(&result.stdout, &result.stderr)?;
    process::exit(result.exit_code);
}

#[derive(Clone, Copy)]
enum JsonKind {
    Http,
    Prompt,
    Agent,
}

fn run_json(kind: JsonKind, args: HookExecSpecArgs, stdin_bytes: &[u8]) -> anyhow::Result<()> {
    let spec = load_spec(&args.spec)?;
    let result = execute_json_hook(kind, &spec, stdin_bytes)?;
    write_json_stdout(&args.target.harness().hook_output(spec.event, &result))?;
    process::exit(0);
}

fn execute_json_hook(
    kind: JsonKind,
    spec: &HookExecutionSpec,
    stdin_bytes: &[u8],
) -> anyhow::Result<NormalizedHookResult> {
    match kind {
        JsonKind::Http => handlers::run_http(spec, stdin_bytes),
        JsonKind::Prompt => handlers::run_prompt(spec, stdin_bytes),
        JsonKind::Agent => handlers::run_agent(spec, stdin_bytes),
    }
}

fn run_dispatch(args: HookDispatchArgs, stdin_bytes: &[u8]) -> anyhow::Result<()> {
    let event = parse_event(&args.event)?;
    let outcome = dispatch(DispatchArgs {
        target: args.target,
        event,
        specs_dir: &args.specs_dir,
        stdin_bytes,
    })?;
    write_json_stdout(&outcome.json)?;
    process::exit(outcome.exit_code);
}

fn parse_event(raw: &str) -> anyhow::Result<ClaudeEvent> {
    ClaudeEvent::from_any_str(raw).ok_or_else(|| anyhow::anyhow!("unknown hook event `{raw}`"))
}

fn run_inject_guidance(args: HookInjectGuidanceArgs) -> anyhow::Result<()> {
    let body = std::fs::read_to_string(&args.file)
        .map_err(|e| anyhow::anyhow!("read guidance file {}: {e}", args.file.display()))?;
    let value = args
        .target
        .harness()
        .guidance_injection_json(&body, &args.event);
    write_json_stdout(&value)?;
    process::exit(0);
}
