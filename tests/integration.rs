use std::fs;
use std::path::{Path, PathBuf};

use agentpack::lockfile::{LockPlugin, LockSkill, PackLock};
use agentpack::paths::{
    cache_dir, cursor_workspace_dir, staging_codex_home_dir, staging_cursor_bundle_dir,
    staging_cursor_home_dir, staging_cursor_pack_plugin_dir, staging_opencode_dir,
    staging_plugins_dir,
};
use agentpack::{run, Cli, Command};
use serial_test::serial;
use tempfile::tempdir;

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
fn sync_stages_full_plugin_and_shadows_contained_skill() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
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
    lock.plugins.push(LockPlugin {
        module: String::new(),
        name: String::new(),
        url: "https://github.com/o/r/tree/main/plugins/foo".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "plugins/foo".into(),
        commit: commit.clone(),
        cache_key: pk.clone(),
    });
    lock.skills.push(LockSkill {
        module: String::new(),
        url: "https://github.com/o/r/tree/main/plugins/foo/skills/bar".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "plugins/foo/skills/bar".into(),
        commit,
        cache_key: sk.clone(),
    });
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
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
    assert!(
        cursor_workspace_dir(&root)
            .join("agents/from-plugin.md")
            .is_file(),
        "project ./.cursor/agents should symlink to staged pack agents for Cursor agent --workspace"
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
    lock.skills.push(LockSkill {
        module: String::new(),
        url: "https://github.com/o/r/tree/main/skills/shared-skill".into(),
        owner: "o".into(),
        repo: "r".into(),
        path: "skills/shared-skill".into(),
        commit: "d".repeat(40),
        cache_key: sk,
    });
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
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
fn sync_converts_markdown_artifacts_per_target_harness() {
    let dir = tempdir().unwrap();
    let root: PathBuf = dir.path().to_path_buf();
    prep_store(&root);
    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
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
    lock.plugins.push(LockPlugin {
        module: String::new(),
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
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
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
    assert!(
        cursor_workspace_dir(&root).join("agents/reviewer.md").is_file(),
        "cursor subagents symlink ./.cursor/agents -> pack agents"
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
    lock.plugins.push(LockPlugin {
        module: String::new(),
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
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
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
    lock.plugins.push(LockPlugin {
        module: String::new(),
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
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
        },
    })
    .unwrap();

    let user_cmd = root.join(".cursor/commands/user-owned.md");
    fs::create_dir_all(user_cmd.parent().unwrap()).unwrap();
    fs::write(&user_cmd, "# user\n").unwrap();

    let mut lock = PackLock::load(&root).unwrap();
    lock.plugins.clear();
    lock.skills.clear();
    lock.save(&root).unwrap();

    run(Cli {
        project_root: Some(root.clone()),
        quiet: true,
        no_progress: true,
        command: Command::Sync {
            dry_run: false,
            verify_only: false,
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
