use std::process;

use crate::hooks::ir::NormalizedHookResult;
use crate::hooks::runtime::bridge::{
    forward_process_output, load_spec, read_stdin_bytes, write_json_stdout, HookExecutionSpec,
};
use crate::hooks::runtime::{agent, command, http, prompt};
use crate::hooks::runtime::translate::to_target_output;

use super::{HookExecArgs, HookExecKind};

pub fn run(args: HookExecArgs) -> anyhow::Result<()> {
    let stdin_bytes = read_stdin_bytes()?;
    let spec = load_spec(&args.spec)?;
    match args.kind {
        HookExecKind::Command => {
            let result = command::execute(&spec, &stdin_bytes)?;
            forward_process_output(&result.stdout, &result.stderr)?;
            process::exit(result.exit_code);
        }
        kind => {
            let result = execute_json_hook(kind, &spec, &stdin_bytes)?;
            write_json_stdout(&to_target_output(args.target, spec.event, &result))?;
            process::exit(0);
        }
    }
}

fn execute_json_hook(
    kind: HookExecKind,
    spec: &HookExecutionSpec,
    stdin_bytes: &[u8],
) -> anyhow::Result<NormalizedHookResult> {
    match kind {
        HookExecKind::Http => http::execute(spec, stdin_bytes),
        HookExecKind::Prompt => prompt::execute(spec, stdin_bytes),
        HookExecKind::Agent => agent::execute(spec, stdin_bytes),
        HookExecKind::Command => unreachable!(),
    }
}
