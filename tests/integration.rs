use std::fs;
use std::path::{Path, PathBuf};

use agentpack::cli::dispatch::run;
use agentpack::cli::{Cli, Command, ModeAction};
use agentpack::harness::HarnessTarget;
use agentpack::lockfile::{LockPackage, PackLock, PackageKind};
use agentpack::mode::filter::EffectiveMode;
use agentpack::paths::{
    agentpack_claude_settings_path, cache_dir, cursor_workspace_dir, project_dot_agents_dir,
    staging_codex_home_dir, staging_codex_home_dir_for_mode, staging_cursor_bundle_dir,
    staging_cursor_home_dir, staging_cursor_pack_plugin_dir, staging_opencode_dir,
    staging_plugins_dir, staging_plugins_dir_for_mode,
};
use agentpack::sync::launch_fingerprint::{
    compute_launch_sync_digest, read_stored_launch_digest, write_launch_sync_state,
};
use serial_test::serial;
use tempfile::tempdir;

struct CwdGuard(PathBuf);

impl Drop for CwdGuard {
    fn drop(&mut self) {
        let _ = std::env::set_current_dir(&self.0);
    }
}

fn prep_store(root: &Path) {
    std::env::set_var("AGENTPACK_HOME", root.join("_test_uap"));
}

#[test]
#[serial]
fn init_writes_pack_lock_and_agentpack_dir() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: Some("myproj".into()),
            version: Some("0.0.1".into()),
        },
    })
    .unwrap();
    assert!(root.join("agentpack.toml").is_file());
    assert!(root.join("pack.lock").is_file());
    assert!(root.join("_test_uap/cache").is_dir());
}

#[test]
#[serial]
fn init_refuses_existing_lock() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();
    let e = run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap_err();
    assert!(e.to_string().contains("agentpack.toml"));
}

#[test]
#[serial]
fn add_lazily_initializes_project_files_in_cwd() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    let _cwd = CwdGuard(std::env::current_dir().unwrap());
    std::env::set_current_dir(&root).unwrap();

    let skill = root.join("local-skill");
    fs::create_dir_all(&skill).unwrap();
    fs::write(skill.join("SKILL.md"), "# local skill\n").unwrap();

    run(Cli {
        project_root: None,
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Add {
            spec: "local-skill".into(),
            no_sync: true,
        },
    })
    .unwrap();

    let manifest = fs::read_to_string(root.join("agentpack.toml")).unwrap();
    assert!(manifest.contains("[dependencies]"));
    assert!(manifest.contains("local-skill = { path = \"local-skill\" }"));
    let lock = PackLock::load(&root).unwrap();
    assert_eq!(lock.lockfile_version, 2);
    assert_eq!(lock.packages.len(), 1);
}

#[test]
#[serial]
fn manifestless_launch_sync_uses_empty_ephemeral_pack() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);

    let ui = agentpack::ui::Ui::new(true, true, false);
    let mode = agentpack::sync::sync_for_launch(&root, None, HarnessTarget::Claude, &ui).unwrap();

    assert_eq!(mode.name(), "default");
    assert!(!root.join("agentpack.toml").exists());
    assert!(!root.join("pack.lock").exists());
    assert!(staging_plugins_dir(&root)
        .unwrap()
        .join("agentpack-bundle/.claude-plugin/plugin.json")
        .is_file());
}

#[test]
#[serial]
fn manifestless_launch_rejects_non_default_mode() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);

    let ui = agentpack::ui::Ui::new(true, true, false);
    let err = agentpack::sync::sync_for_launch(&root, Some("custom"), HarnessTarget::Claude, &ui)
        .unwrap_err();
    assert!(err.to_string().contains("unknown mode: custom"));
}

#[test]
#[serial]
fn sync_still_requires_existing_project_files() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    let _cwd = CwdGuard(std::env::current_dir().unwrap());
    std::env::set_current_dir(&root).unwrap();

    let err = run(Cli {
        project_root: None,
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap_err();

    assert!(err.to_string().contains("no pack.lock found"));
}

#[test]
#[serial]
fn launch_sync_state_roundtrip_under_isolated_home() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    fs::write(
        root.join("agentpack.toml"),
        "name = \"t\"\nversion = \"1\"\n",
    )
    .unwrap();
    fs::write(
        root.join("pack.lock"),
        "lockfile-version = 2\n\n[meta]\nname = \"t\"\nversion = \"1\"\n",
    )
    .unwrap();

    let mode = EffectiveMode::implicit_default();
    let d = compute_launch_sync_digest(&root, &mode, None).unwrap();
    assert!(read_stored_launch_digest(&root, mode.name())
        .unwrap()
        .is_none());
    write_launch_sync_state(&root, mode.name(), &d).unwrap();
    assert_eq!(
        read_stored_launch_digest(&root, mode.name())
            .unwrap()
            .as_deref(),
        Some(d.as_str())
    );
}

#[test]
#[serial]
fn sync_stages_full_plugin_and_shadows_contained_skill() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);

    // `agentpack agent` keys the Cursor workspace (and its `.cursor/agents` overlay) off the CWD,
    // not the pack root — so simulate the user being `cd`'d into their project. Restored on drop
    // (before `dir`, since locals drop in reverse order).
    struct CwdGuard(PathBuf);
    impl Drop for CwdGuard {
        fn drop(&mut self) {
            let _ = std::env::set_current_dir(&self.0);
        }
    }
    let _cwd = CwdGuard(std::env::current_dir().unwrap());
    std::env::set_current_dir(&root).unwrap();
    let ws = std::env::current_dir().unwrap();
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let pk = "a".repeat(64);
    let sk = "b".repeat(64);
    let cache = cache_dir().unwrap();
    fs::create_dir_all(cache.join(&pk).join(".claude-plugin")).unwrap();
    fs::write(
        cache.join(&pk).join(".claude-plugin/plugin.json"),
        r#"{"name":"t","version":"1.0.0"}"#,
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join("agents")).unwrap();
    fs::write(cache.join(&pk).join("agents").join("from-plugin.md"), "# p").unwrap();
    fs::create_dir_all(cache.join(&sk)).unwrap();
    fs::write(cache.join(&sk).join("SKILL.md"), "# nested skill").unwrap();

    let commit = "c".repeat(40);
    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.push(LockPackage {
        module: String::new(),
        direct: true,
        kind: PackageKind::Plugin,
        name: String::new(),
        url: "https://github.com/o/r/tree/main/plugins/foo".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "plugins/foo".into(),
        commit: commit.clone(),
        cache_key: pk.clone(),
    });
    lock.packages.push(LockPackage {
        module: String::new(),
        direct: true,
        kind: PackageKind::Skill,
        url: "https://github.com/o/r/tree/main/plugins/foo/skills/bar".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "plugins/foo/skills/bar".into(),
        commit,
        cache_key: sk.clone(),
        name: String::new(),
    });
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    let stage = staging_plugins_dir(&root).unwrap();
    let hubs: Vec<_> = fs::read_dir(&stage)
        .unwrap()
        .filter_map(|e| e.ok())
        .map(|e| e.path())
        .filter(|p| p.is_dir())
        .collect();
    assert_eq!(
        hubs.len(),
        1,
        "single merged agentpack-bundle; skill under plugin path is shadowed"
    );
    let bundle = stage.join("agentpack-bundle");
    assert!(
        bundle.join(".claude-plugin/plugin.json").is_file(),
        "bundle should expose manifest"
    );
    assert!(
        bundle.join("agents/from-plugin.md").is_file(),
        "plugin agents/ merged into bundle"
    );
    let opencode_root = staging_opencode_dir(&root).unwrap();
    assert!(
        opencode_root.join("agents/from-plugin.md").is_file(),
        "plugin agents/ merged into opencode root"
    );
    let codex_home = staging_codex_home_dir(&root).unwrap();
    assert!(
        !codex_home.join("agents/from-plugin.md").exists(),
        "codex staging should only expose portable skills, not Claude-only agent dirs"
    );
    assert!(
        codex_home.join("skills/from-plugin/SKILL.md").is_file(),
        "codex staging should convert agents into portable skills"
    );
    let cursor_cfg = staging_cursor_bundle_dir(&root).unwrap();
    let cursor_pack = staging_cursor_pack_plugin_dir(&root).unwrap();
    assert!(cursor_cfg.join(".cursor-plugin/marketplace.json").is_file());
    assert!(cursor_pack.join(".cursor-plugin/plugin.json").is_file());
    assert!(cursor_pack.join("README.md").is_file());
    assert!(
        cursor_pack.join("agents/from-plugin.md").is_file(),
        "plugin agents/ merged into staged cursor plugin tree"
    );
    let cursor_home = staging_cursor_home_dir(&root).unwrap();
    assert!(
        cursor_home.join(".cursor/agents/from-plugin.md").is_file(),
        "fake HOME .cursor should symlink to pack agents"
    );
    // Bare `agentpack sync` is target-agnostic, so the project-side `./.cursor/agents` symlink
    // (only needed by `agentpack agent`) should not be materialized here.
    assert!(
        !ws.join(".cursor/agents").exists(),
        "bare sync must not drop the Cursor workspace overlay into the project"
    );

    // Re-running sync with `HarnessTarget::Cursor` materializes the workspace symlink, and
    // following up with `HarnessTarget::Claude` cleans it back up — proving the overlay is
    // truly target-scoped, not a side effect of having Cursor content cached.
    let ui = agentpack::ui::Ui::new(true, true, false);
    agentpack::sync::run_sync(
        &root,
        false,
        false,
        false,
        None,
        Some(agentpack::harness::HarnessTarget::Cursor),
        &ui,
    )
    .unwrap();
    assert!(
        ws.join(".cursor/agents/from-plugin.md").is_file(),
        "Cursor target should drop the ./.cursor/agents symlink"
    );

    agentpack::sync::run_sync(
        &root,
        false,
        false,
        false,
        None,
        Some(agentpack::harness::HarnessTarget::Claude),
        &ui,
    )
    .unwrap();
    assert!(
        !ws.join(".cursor/agents").exists(),
        "Claude target should leave the Cursor workspace overlay absent"
    );
    assert!(
        !root.join(".agents/plugins/agentpack-bundle").exists(),
        "Claude target must not create the Antigravity workspace overlay"
    );

    agentpack::sync::run_sync(
        &root,
        false,
        false,
        false,
        None,
        Some(agentpack::harness::HarnessTarget::Agy),
        &ui,
    )
    .unwrap();
    assert!(
        root.join(".agents/plugins/agentpack-bundle").exists(),
        "Agy target should create the workspace plugin symlink"
    );
}

#[test]
#[serial]
fn sync_stages_bare_skills_for_all_launchers() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let sk = "c".repeat(64);
    let cache = cache_dir().unwrap();
    fs::create_dir_all(cache.join(&sk)).unwrap();
    fs::write(cache.join(&sk).join("SKILL.md"), "# shared skill").unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.push(LockPackage {
        module: String::new(),
        direct: true,
        kind: PackageKind::Skill,
        url: "https://github.com/o/r/tree/main/skills/shared-skill".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "skills/shared-skill".into(),
        commit: "d".repeat(40),
        cache_key: sk,
        name: String::new(),
    });
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    for skill_root in [
        staging_plugins_dir(&root)
            .unwrap()
            .join("agentpack-bundle")
            .join("skills")
            .join("shared-skill"),
        staging_opencode_dir(&root)
            .unwrap()
            .join("skills")
            .join("shared-skill"),
        staging_codex_home_dir(&root)
            .unwrap()
            .join("skills")
            .join("shared-skill"),
        staging_cursor_pack_plugin_dir(&root)
            .unwrap()
            .join("skills")
            .join("shared-skill"),
    ] {
        assert!(
            skill_root.join("SKILL.md").is_file(),
            "expected staged skill at {}",
            skill_root.display()
        );
    }
}

#[test]
#[serial]
fn sync_stages_bare_skill_under_lockfile_slug_when_frontmatter_name_differs() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let sk = "d".repeat(64);
    let cache = cache_dir().unwrap();
    fs::create_dir_all(cache.join(&sk)).unwrap();
    fs::write(
        cache.join(&sk).join("SKILL.md"),
        "---\nname: vercel-react-best-practices\ndescription: React guidance\n---\n\n# React Best Practices\n",
    )
    .unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.push(LockPackage {
        module: String::new(),
        direct: true,
        kind: PackageKind::Skill,
        url: "https://github.com/vercel-labs/agent-skills/tree/main/skills/react-best-practices"
            .into(),
        owner: "vercel-labs".into(),
        repo: "agent-skills".into(),
        path: "skills/react-best-practices".into(),
        commit: "e".repeat(40),
        cache_key: sk,
        name: String::new(),
    });
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    let staged = staging_plugins_dir(&root)
        .unwrap()
        .join("agentpack-bundle")
        .join("skills")
        .join("react-best-practices")
        .join("SKILL.md");
    let contents = fs::read_to_string(&staged).unwrap();
    assert!(staged.is_file());
    assert!(contents.contains("name: vercel-react-best-practices"));
}

/// Plugin trees must merge non-`.md` assets under `commands` / `skills` / etc. for **both** Cursor
/// and Claude bundles (raw subtree merge + markdown overlay).
#[test]
#[serial]
fn sync_stages_cursor_and_claude_plugin_with_skill_support_and_command_sidecars() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let pk = "7".repeat(64);
    let cache = cache_dir().unwrap();
    fs::create_dir_all(cache.join(&pk).join(".cursor-plugin")).unwrap();
    fs::write(
        cache.join(&pk).join(".cursor-plugin/plugin.json"),
        r#"{"name":"rust-dev-like","version":"1.0.0","displayName":"rust-dev-like"}"#,
    )
    .unwrap();
    let skill_dir = cache.join(&pk).join("skills").join("rust-skill");
    fs::create_dir_all(skill_dir.join("evals")).unwrap();
    fs::write(
        skill_dir.join("SKILL.md"),
        "---\nname: rust-skill\ndescription: Rust helpers\n---\n\nBody.\n",
    )
    .unwrap();
    fs::write(
        skill_dir.join("evals").join("evals.json"),
        "{\"version\":1}\n",
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join("commands")).unwrap();
    fs::write(
        cache.join(&pk).join("commands").join("sidecar.txt"),
        "binary-friendly sidecar\n",
    )
    .unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.push(LockPackage {
        module: String::new(),
        direct: true,
        kind: PackageKind::Plugin,
        name: String::new(),
        url: "https://github.com/o/r/tree/main/plugins/rust-dev".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "plugins/rust-dev".into(),
        commit: "8".repeat(40),
        cache_key: pk,
    });
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    let cursor_pack = staging_cursor_pack_plugin_dir(&root).unwrap();
    let staged_skill = cursor_pack.join("skills/rust-skill");
    assert!(staged_skill.join("SKILL.md").is_file());
    assert_eq!(
        fs::read_to_string(staged_skill.join("evals/evals.json")).unwrap(),
        "{\"version\":1}\n"
    );
    assert_eq!(
        fs::read_to_string(cursor_pack.join("commands/sidecar.txt")).unwrap(),
        "binary-friendly sidecar\n"
    );

    let cursor_home = staging_cursor_home_dir(&root).unwrap();
    assert!(cursor_home
        .join(".cursor/skills/rust-skill/evals/evals.json")
        .is_file());
    assert!(cursor_home.join(".cursor/commands/sidecar.txt").is_file());

    let claude_bundle = staging_plugins_dir(&root).unwrap().join("agentpack-bundle");
    assert!(claude_bundle.join("skills/rust-skill/SKILL.md").is_file());
    assert_eq!(
        fs::read_to_string(claude_bundle.join("skills/rust-skill/evals/evals.json")).unwrap(),
        "{\"version\":1}\n"
    );
    assert_eq!(
        fs::read_to_string(claude_bundle.join("commands/sidecar.txt")).unwrap(),
        "binary-friendly sidecar\n"
    );
}

#[test]
#[serial]
fn sync_converts_markdown_artifacts_per_target_harness() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let pk = "e".repeat(64);
    let cache = cache_dir().unwrap();
    fs::create_dir_all(cache.join(&pk).join(".claude-plugin")).unwrap();
    fs::write(
        cache.join(&pk).join(".claude-plugin/plugin.json"),
        r#"{"name":"portable","version":"1.0.0"}"#,
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join(".cursor/commands")).unwrap();
    fs::write(
        cache
            .join(&pk)
            .join(".cursor/commands")
            .join("review-code.md"),
        "# Review code\n\nCheck the diff carefully.\n",
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join(".opencode/commands")).unwrap();
    fs::write(
        cache.join(&pk).join(".opencode/commands").join("test.md"),
        "---\ndescription: Run tests with coverage\nagent: build\nmodel: anthropic/claude-3-5-sonnet-20241022\n---\n\nRun the full test suite.\n",
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join(".cursor/rules")).unwrap();
    fs::write(
        cache.join(&pk).join(".cursor/rules").join("typescript.mdc"),
        "---\ndescription: TypeScript standards\nglobs: **/*.ts\nalwaysApply: false\n---\n\n# TypeScript\n\nUse strict types.\n",
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join(".claude/agents")).unwrap();
    fs::write(
        cache.join(&pk).join(".claude/agents").join("reviewer.md"),
        "---\nname: reviewer\ndescription: Reviews code changes\nallowed-tools: Read, Grep\n---\n\nReview the modified files.\n",
    )
    .unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.push(LockPackage {
        module: String::new(),
        direct: true,
        kind: PackageKind::Plugin,
        name: String::new(),
        url: "https://github.com/o/r/tree/main/portable".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "portable".into(),
        commit: "f".repeat(40),
        cache_key: pk,
    });
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    let claude_bundle = staging_plugins_dir(&root).unwrap().join("agentpack-bundle");
    let claude_command = fs::read_to_string(claude_bundle.join("commands/review-code.md")).unwrap();
    assert!(claude_command.contains("name: review-code"));
    assert!(claude_command.contains("description: Review code"));
    assert!(claude_command.contains("Check the diff carefully."));

    let opencode_root = staging_opencode_dir(&root).unwrap();
    let opencode_command =
        fs::read_to_string(opencode_root.join("commands/review-code.md")).unwrap();
    assert!(opencode_command.contains("description: Review code"));
    assert!(!opencode_command.contains("name: review-code"));
    assert!(opencode_command.contains("Check the diff carefully."));

    let cursor_pack = staging_cursor_pack_plugin_dir(&root).unwrap();
    let cursor_command = fs::read_to_string(cursor_pack.join("commands/test.md")).unwrap();
    assert!(cursor_command.contains("name: test"));
    assert!(cursor_command.contains("description: Run tests with coverage"));
    assert!(cursor_command.contains("Run the full test suite."));
    let cursor_rule = fs::read_to_string(cursor_pack.join("rules/typescript.mdc")).unwrap();
    assert!(cursor_rule.contains("description: TypeScript standards"));
    assert!(cursor_rule.contains("globs:"));
    assert!(cursor_rule.contains("**/*.ts"));
    let cursor_agent = fs::read_to_string(cursor_pack.join("agents/reviewer.md")).unwrap();
    assert!(cursor_agent.contains("name: reviewer"));
    assert!(cursor_agent.contains("description: Reviews code changes"));
    assert!(!cursor_agent.contains("allowed-tools"));
    assert!(
        !cursor_workspace_dir(&root)
            .join("commands/test.md")
            .exists(),
        "cursor pack commands must not be written into the project workspace"
    );
    // Bare `Command::Sync` (no harness target) must not write the `./.cursor/agents` symlink.
    // That overlay belongs to `agentpack agent` and is exercised separately.
    assert!(
        !cursor_workspace_dir(&root).join("agents").exists(),
        "bare sync must not drop the Cursor workspace overlay into the project"
    );

    let codex_home = staging_codex_home_dir(&root).unwrap();
    let codex_command_skill =
        fs::read_to_string(codex_home.join("skills/review-code/SKILL.md")).unwrap();
    assert!(codex_command_skill.contains("name: review-code"));
    assert!(codex_command_skill.contains("disable-model-invocation: true"));
    let codex_rule_skill =
        fs::read_to_string(codex_home.join("skills/typescript/SKILL.md")).unwrap();
    assert!(codex_rule_skill.contains("Original Cursor globs"));
}

#[test]
#[serial]
fn sync_leaves_project_cursor_files_alone_when_pack_overlaps_names() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let pk = "1".repeat(64);
    let cache = cache_dir().unwrap();
    fs::create_dir_all(cache.join(&pk).join(".claude-plugin")).unwrap();
    fs::write(
        cache.join(&pk).join(".claude-plugin/plugin.json"),
        r#"{"name":"portable","version":"1.0.0"}"#,
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join(".cursor/commands")).unwrap();
    fs::write(
        cache.join(&pk).join(".cursor/commands").join("shipit.md"),
        "# Ship it\n\nRun the release checklist.\n",
    )
    .unwrap();

    fs::create_dir_all(root.join(".cursor/commands")).unwrap();
    fs::write(
        root.join(".cursor/commands/shipit.md"),
        "# Existing project command\n",
    )
    .unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.push(LockPackage {
        module: String::new(),
        direct: true,
        kind: PackageKind::Plugin,
        name: String::new(),
        url: "https://github.com/o/r/tree/main/portable".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "portable".into(),
        commit: "2".repeat(40),
        cache_key: pk,
    });
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();
    assert_eq!(
        fs::read_to_string(root.join(".cursor/commands/shipit.md")).unwrap(),
        "# Existing project command\n",
        "project .cursor file must not be overwritten by pack sync"
    );
    let staged_shipit = staging_cursor_pack_plugin_dir(&root)
        .unwrap()
        .join("commands/shipit.md");
    assert!(
        staged_shipit.is_file(),
        "pack command should exist in staged cursor root: {}",
        staged_shipit.display()
    );
}

#[test]
#[serial]
fn sync_does_not_remove_user_cursor_files_when_pack_entries_removed() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let pk = "3".repeat(64);
    let cache = cache_dir().unwrap();
    fs::create_dir_all(cache.join(&pk).join(".claude-plugin")).unwrap();
    fs::write(
        cache.join(&pk).join(".claude-plugin/plugin.json"),
        r#"{"name":"portable","version":"1.0.0"}"#,
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join(".cursor/commands")).unwrap();
    fs::write(
        cache.join(&pk).join(".cursor/commands").join("shipit.md"),
        "# Ship it\n\nRun the release checklist.\n",
    )
    .unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.push(LockPackage {
        module: String::new(),
        direct: true,
        kind: PackageKind::Plugin,
        name: String::new(),
        url: "https://github.com/o/r/tree/main/portable".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "portable".into(),
        commit: "4".repeat(40),
        cache_key: pk,
    });
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    let user_cmd = root.join(".cursor/commands/user-owned.md");
    fs::create_dir_all(user_cmd.parent().unwrap()).unwrap();
    fs::write(&user_cmd, "# user\n").unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.clear();
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();
    assert!(
        !staging_cursor_pack_plugin_dir(&root)
            .unwrap()
            .join("commands/shipit.md")
            .exists(),
        "staged pack command should be removed when lock has no plugins"
    );
    assert!(
        user_cmd.is_file(),
        "sync must not delete unrelated project .cursor files"
    );
}

#[test]
#[serial]
fn sync_merges_dot_agents_into_staging() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let da = project_dot_agents_dir(&root);
    fs::create_dir_all(da.join("rules/nested")).unwrap();
    fs::write(
        da.join("rules/nested/standards.mdc"),
        "---\nalwaysApply: true\n---\n\n# Dot agents rule\n",
    )
    .unwrap();
    fs::create_dir_all(da.join("agents")).unwrap();
    fs::write(da.join("agents/local-sub.md"), "# Local subagent\n").unwrap();
    fs::create_dir_all(da.join("skills/dot-skill")).unwrap();
    fs::write(
        da.join("skills/dot-skill/SKILL.md"),
        "---\nname: dot-skill\ndescription: from dot agents\n---\n",
    )
    .unwrap();
    fs::write(da.join("AGENTS.md"), "# Codex agents from dot\n").unwrap();
    fs::write(da.join("CLAUDE.md"), "# Claude project from dot\n").unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    let bundle = staging_plugins_dir(&root).unwrap().join("agentpack-bundle");
    assert!(
        bundle
            .join("rules/dot-agents--nested--standards.mdc")
            .is_file(),
        "claude bundle should include flattened dot-agents rules"
    );
    assert!(bundle.join("agents/local-sub.md").is_file());
    assert!(bundle.join("skills/dot-skill/SKILL.md").is_file());
    assert!(fs::read_to_string(bundle.join("CLAUDE.md"))
        .unwrap()
        .contains("Claude project from dot"));

    // OpenCode natively reads `.agents/` from the workspace, so dot-agents content
    // is NOT merged into OpenCode staging.

    let codex_home = staging_codex_home_dir(&root).unwrap();
    assert!(fs::read_to_string(codex_home.join("AGENTS.md"))
        .unwrap()
        .contains("Codex agents from dot"));
    assert!(codex_home.join("skills/dot-skill/SKILL.md").is_file());

    // Cursor natively reads `.agents/` from the workspace, so dot-agents content
    // is NOT merged into Cursor staging.
}

#[test]
#[serial]
fn sync_applies_default_and_selected_modes_to_staging() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let cache_key = "d".repeat(64);
    let cache_root = cache_dir().unwrap().join(&cache_key);
    fs::create_dir_all(cache_root.join(".claude-plugin")).unwrap();
    fs::write(
        cache_root.join(".claude-plugin/plugin.json"),
        r#"{"name":"design-pack","version":"1.0.0"}"#,
    )
    .unwrap();
    fs::create_dir_all(cache_root.join("commands")).unwrap();
    fs::write(cache_root.join("commands/noisy.md"), "# noisy\n").unwrap();
    fs::write(cache_root.join("commands/useful.md"), "# useful\n").unwrap();
    fs::create_dir_all(cache_root.join("rules")).unwrap();
    fs::write(
        cache_root.join("rules/always.mdc"),
        "---\nalwaysApply: true\n---\n\n# Filtered Rule\n",
    )
    .unwrap();
    fs::write(
        cache_root.join("mcp.json"),
        r#"{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem"]}}}"#,
    )
    .unwrap();

    let dot_agents = project_dot_agents_dir(&root);
    fs::create_dir_all(dot_agents.join("skills/local-skill")).unwrap();
    fs::write(
        dot_agents.join("skills/local-skill/SKILL.md"),
        "---\nname: local-skill\ndescription: local\n---\n",
    )
    .unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.push(LockPackage {
        module: "github.com/acme/design-pack".into(),
        direct: true,
        kind: PackageKind::Plugin,
        url: "https://github.com/acme/design-pack/tree/main".into(),
        owner: "acme".into(),
        repo: "design-pack".into(),
        path: String::new(),
        commit: "c".repeat(40),
        cache_key: cache_key.clone(),
        name: String::new(),
    });
    lock.save(&root).unwrap();

    // Default mode is read-only (base=all, no selectors); plain sync stages every
    // cached file untouched.
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    let default_bundle = staging_plugins_dir(&root).unwrap().join("agentpack-bundle");
    assert!(default_bundle.join("commands/useful.md").is_file());
    assert!(default_bundle.join("commands/noisy.md").is_file());

    // Editing `default` is rejected — read-only.
    let default_edit = run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Mode {
            action: ModeAction::Disable {
                name: "default".into(),
                selectors: vec!["package-path:github.com/acme/design-pack:commands/noisy.md".into()],
            },
        },
    });
    assert!(
        default_edit.is_err(),
        "default mode should be read-only and reject selector edits"
    );

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Mode {
            action: ModeAction::Create {
                name: "design".into(),
            },
        },
    })
    .unwrap();
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Mode {
            action: ModeAction::Disable {
                name: "design".into(),
                selectors: vec![
                    "package-path:github.com/acme/design-pack:commands/noisy.md".into(),
                    "package-path:github.com/acme/design-pack:rules/always.mdc".into(),
                    "mcp:filesystem".into(),
                    ".agents:skills/local-skill/SKILL.md".into(),
                ],
            },
        },
    })
    .unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: Some("design".into()),
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    let design_bundle = staging_plugins_dir_for_mode(&root, "design")
        .unwrap()
        .join("agentpack-bundle");
    assert!(design_bundle.is_dir());
    assert!(design_bundle.join("commands/useful.md").is_file());
    assert!(!design_bundle.join("commands/noisy.md").exists());
    assert!(
        !design_bundle.join(".mcp.json").exists(),
        "filtered MCP contributions should not be staged"
    );

    let design_codex = staging_codex_home_dir_for_mode(&root, "design").unwrap();
    assert!(
        !design_codex.join("skills/local-skill/SKILL.md").exists(),
        "filtered .agents skill should not be merged into codex staging"
    );
    let guidance = fs::read_to_string(design_codex.join("AGENTS.md")).unwrap_or_default();
    assert!(
        !guidance.contains("Filtered Rule"),
        "filtered always-apply rule should not be injected"
    );
}

#[test]
#[serial]
#[ignore = "network: resolves GitHub API and downloads tarball"]
fn add_real_github_skill() {
    let _ = tracing_subscriber::fmt::try_init();
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: false,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();
    run(Cli {
        project_root: Some(root.clone()),
        quiet: false,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Add {
            spec: "https://github.com/anthropics/skills/tree/main/skills/canvas-design".into(),
            no_sync: false,
        },
    })
    .unwrap();
    assert!(root
        .join("_test_uap/cache")
        .read_dir()
        .unwrap()
        .next()
        .is_some());
}

#[test]
#[serial]
fn sync_stages_hooks_for_all_harnesses() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let pk = "9".repeat(64);
    let cache = cache_dir().unwrap();
    fs::create_dir_all(cache.join(&pk).join(".claude-plugin")).unwrap();
    fs::write(
        cache.join(&pk).join(".claude-plugin/plugin.json"),
        r#"{"name":"hook-pack","version":"1.0.0"}"#,
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join("scripts")).unwrap();
    fs::write(
        cache.join(&pk).join("scripts/validate.sh"),
        "#!/bin/sh\necho ok\n",
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join("hooks")).unwrap();
    fs::write(
        cache.join(&pk).join("hooks/hooks.json"),
        r#"{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "./scripts/validate.sh" },
          { "type": "http", "url": "https://example.com/hook", "method": "POST" }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          { "type": "prompt", "prompt": "Summarize whether the task is complete." }
        ]
      }
    ]
  }
}"#,
    )
    .unwrap();

    let dot_agents = project_dot_agents_dir(&root);
    fs::create_dir_all(dot_agents.join("hooks")).unwrap();
    fs::write(
        dot_agents.join("hooks/hooks.json"),
        r#"{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          { "type": "prompt", "prompt": "Capture the user intent before execution." }
        ]
      }
    ]
  }
}"#,
    )
    .unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.push(LockPackage {
        module: "github.com/o/r/plugins/hook-pack".into(),
        direct: true,
        kind: PackageKind::Plugin,
        url: "https://github.com/o/r/tree/main/plugins/hook-pack".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "plugins/hook-pack".into(),
        commit: "f".repeat(40),
        cache_key: pk.clone(),
        name: String::new(),
    });
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    let bundle = staging_plugins_dir(&root).unwrap().join("agentpack-bundle");
    let claude_hooks = fs::read_to_string(bundle.join("hooks/hooks.json")).unwrap();
    assert!(claude_hooks.contains("agentpack hook-exec command"));
    assert!(claude_hooks.contains("\"type\": \"http\""));
    assert!(claude_hooks.contains("Capture the user intent before execution."));
    assert!(
        bundle
            .join(format!("hooks/_packages/{pk}/package/scripts/validate.sh"))
            .is_file(),
        "hook package assets should be namespaced under the bundle hooks root"
    );

    let cursor_hooks: serde_json::Value = serde_json::from_str(
        &fs::read_to_string(
            staging_cursor_pack_plugin_dir(&root)
                .unwrap()
                .join("hooks/hooks.json"),
        )
        .unwrap(),
    )
    .unwrap();
    assert_eq!(cursor_hooks["version"], 1);
    // Cursor now uses blanket dispatcher entries — one `type: command` per (step, Claude event)
    // invoking `agentpack hook-exec dispatch`, which handles matching and handler execution.
    assert!(cursor_hooks["hooks"]["preToolUse"]
        .as_array()
        .unwrap()
        .iter()
        .any(|entry| entry["command"]
            .as_str()
            .is_some_and(|command| command.contains("hook-exec dispatch"))
            && entry["type"] == "command"));
    assert!(cursor_hooks["hooks"]["stop"]
        .as_array()
        .unwrap()
        .iter()
        .any(|entry| entry["command"]
            .as_str()
            .is_some_and(|command| command.contains("hook-exec dispatch"))));

    let codex_hooks =
        fs::read_to_string(staging_codex_home_dir(&root).unwrap().join("hooks.json")).unwrap();
    assert!(codex_hooks.contains("PreToolUse"));
    assert!(codex_hooks.contains("hook-exec http"));

    let opencode_root = staging_opencode_dir(&root).unwrap();
    let opencode_config: serde_json::Value =
        serde_json::from_str(&fs::read_to_string(opencode_root.join("opencode.json")).unwrap())
            .unwrap();
    assert!(opencode_root
        .join("plugins/agentpack-hooks/index.js")
        .is_file());
    assert!(opencode_root
        .join("plugins/agentpack-hooks/config.json")
        .is_file());
    assert!(opencode_config["plugin"]
        .as_array()
        .unwrap()
        .iter()
        .any(|entry| entry == "./plugins/agentpack-hooks/index.js"));
}

#[test]
#[serial]
fn sync_skips_unsupported_cursor_matcher_gracefully() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    let pk = "8".repeat(64);
    let cache = cache_dir().unwrap();
    fs::create_dir_all(cache.join(&pk).join(".claude-plugin")).unwrap();
    fs::write(
        cache.join(&pk).join(".claude-plugin/plugin.json"),
        r#"{"name":"glob-pack","version":"1.0.0"}"#,
    )
    .unwrap();
    fs::create_dir_all(cache.join(&pk).join("hooks")).unwrap();
    fs::write(
        cache.join(&pk).join("hooks/hooks.json"),
        r#"{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Glob",
        "hooks": [
          { "type": "command", "command": "echo blocked" }
        ]
      }
    ]
  }
}"#,
    )
    .unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.packages.push(LockPackage {
        module: "github.com/o/r/plugins/glob-pack".into(),
        direct: true,
        kind: PackageKind::Plugin,
        url: "https://github.com/o/r/tree/main/plugins/glob-pack".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "plugins/glob-pack".into(),
        commit: "g".repeat(40),
        cache_key: pk,
        name: String::new(),
    });
    lock.save(&root).unwrap();

    // Glob-only matcher has no Cursor equivalent — sync should succeed
    // (hook is gracefully skipped with a diagnostic, not a hard error).
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();
}

#[test]
#[serial]
fn sync_disables_attribution_in_all_supported_harnesses_by_default() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    std::env::remove_var("AGENTPACK_KEEP_ATTRIBUTION");
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();

    // Claude attribution overlay (passed via `claude --settings <path>`). Lives under
    // $AGENTPACK_HOME, NOT under per-project staging — Claude Code namespaces credentials by
    // CLAUDE_CONFIG_DIR, so a per-project path would forget login on every project switch.
    let claude_settings = agentpack_claude_settings_path().unwrap();
    let meta = fs::symlink_metadata(&claude_settings).unwrap();
    assert!(
        !meta.file_type().is_symlink(),
        "claude --settings overlay must not be a symlink"
    );
    let v: serde_json::Value =
        serde_json::from_str(&fs::read_to_string(&claude_settings).unwrap()).unwrap();
    assert_eq!(v["attribution"]["commit"], "");
    assert_eq!(v["attribution"]["pr"], "");
    assert_eq!(v["includeCoAuthoredBy"], false);

    // Codex home: <codex_home>/config.toml
    let codex_config = staging_codex_home_dir(&root).unwrap().join("config.toml");
    let codex_toml: toml::Value =
        toml::from_str(&fs::read_to_string(&codex_config).unwrap()).unwrap();
    assert_eq!(codex_toml["commit_attribution"].as_str(), Some(""));

    // Cursor pack root and bundle root: cli-config.json
    for cursor_root in [
        staging_cursor_bundle_dir(&root).unwrap(),
        staging_cursor_pack_plugin_dir(&root).unwrap(),
    ] {
        let cfg_path = cursor_root.join("cli-config.json");
        let cfg: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(&cfg_path).unwrap()).unwrap();
        assert_eq!(cfg["attribution"]["attributeCommitsToAgent"], false);
        assert_eq!(cfg["attribution"]["attributePRsToAgent"], false);
    }

    // Cursor fake-home cli-config.json must be a real file (not a symlink), with attribution off.
    let fake_cli = staging_cursor_home_dir(&root)
        .unwrap()
        .join(".cursor/cli-config.json");
    let meta = fs::symlink_metadata(&fake_cli).unwrap();
    assert!(
        !meta.file_type().is_symlink(),
        "fake-home cli-config.json must not be a symlink to ~/.cursor"
    );
    let fake_cfg: serde_json::Value =
        serde_json::from_str(&fs::read_to_string(&fake_cli).unwrap()).unwrap();
    assert_eq!(fake_cfg["attribution"]["attributeCommitsToAgent"], false);
    assert_eq!(fake_cfg["attribution"]["attributePRsToAgent"], false);

    // OpenCode: opencode.json `instructions[]` references the staged file.
    let opencode_root = staging_opencode_dir(&root).unwrap();
    let opencode_json: serde_json::Value =
        serde_json::from_str(&fs::read_to_string(opencode_root.join("opencode.json")).unwrap())
            .unwrap();
    let instructions = opencode_json["instructions"].as_array().unwrap();
    assert!(instructions
        .iter()
        .any(|x| x.as_str() == Some("agentpack-no-attribution.md")));
    assert!(opencode_root.join("agentpack-no-attribution.md").is_file());
}

#[test]
#[serial]
fn sync_keeps_attribution_when_env_opt_in() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    std::env::set_var("AGENTPACK_KEEP_ATTRIBUTION", "1");
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Init {
            name: None,
            version: None,
        },
    })
    .unwrap();
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        yolo: false,
        mode: None,
        debug: false,
        proxy: false,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
            update_lock: false,
        },
    })
    .unwrap();
    std::env::remove_var("AGENTPACK_KEEP_ATTRIBUTION");

    // Claude attribution overlay must be absent so the launcher omits `--settings`.
    let claude_settings = agentpack_claude_settings_path().unwrap();
    assert!(
        !claude_settings.exists(),
        "claude --settings overlay must not exist when AGENTPACK_KEEP_ATTRIBUTION=1"
    );

    // Codex: commit_attribution must not be present.
    let codex_config = staging_codex_home_dir(&root).unwrap().join("config.toml");
    if codex_config.is_file() {
        let codex_toml: toml::Value =
            toml::from_str(&fs::read_to_string(&codex_config).unwrap()).unwrap();
        assert!(codex_toml.get("commit_attribution").is_none());
    }

    // OpenCode: no instructions entry referencing our file.
    let opencode_json_path = staging_opencode_dir(&root).unwrap().join("opencode.json");
    let opencode_json: serde_json::Value =
        serde_json::from_str(&fs::read_to_string(&opencode_json_path).unwrap()).unwrap();
    if let Some(arr) = opencode_json.get("instructions").and_then(|v| v.as_array()) {
        assert!(!arr
            .iter()
            .any(|x| x.as_str() == Some("agentpack-no-attribution.md")));
    }
}
