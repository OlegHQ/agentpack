use std::collections::{BTreeMap, BTreeSet};
use std::io;
use std::path::{Path, PathBuf};

use crossterm::event::{
    self, DisableMouseCapture, EnableMouseCapture, Event, KeyCode, KeyModifiers,
};
use crossterm::execute;
use crossterm::terminal::{
    disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen,
};
use ratatui::backend::CrosstermBackend;
use ratatui::layout::{Alignment, Constraint, Direction, Layout, Rect};
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, List, ListItem, ListState, Paragraph, Wrap};
use ratatui::Terminal;

use crate::error::{AgentpackError, Result};
use crate::manifest::AgentpackManifest;

use super::catalog::CapabilityCatalog;
use super::filter::EffectiveMode;
use super::selectors::Selector;
use super::{is_reserved_mode, validate_mode_name, ModeBase, ModeDefinition, DEFAULT_MODE_NAME};

/// Pure reducer that owns the in-memory modes map. Kept identical to the
/// pre-ratatui design so its unit tests continue to exercise the core
/// semantics (default-is-reserved, selector canonicalisation, dirty flag).
#[derive(Clone, Debug)]
pub struct ModeEditorState {
    pub selected_mode: String,
    pub modes: BTreeMap<String, ModeDefinition>,
    pub dirty: bool,
}

impl ModeEditorState {
    pub fn load(manifest: &AgentpackManifest, selected_mode: Option<&str>) -> Result<Self> {
        let selected_mode = selected_mode.unwrap_or(DEFAULT_MODE_NAME);
        let mut modes = manifest.explicit_modes().clone();
        modes
            .entry(DEFAULT_MODE_NAME.into())
            .or_insert_with(ModeDefinition::implicit_default);
        if !modes.contains_key(selected_mode) {
            return Err(AgentpackError::Mode(format!(
                "unknown mode: {selected_mode}"
            )));
        }
        Ok(Self {
            selected_mode: selected_mode.to_string(),
            modes,
            dirty: false,
        })
    }

    pub fn selected_definition(&self) -> &ModeDefinition {
        self.modes
            .get(&self.selected_mode)
            .expect("selected mode should always exist")
    }

    pub fn create_mode(&mut self, name: &str) -> Result<()> {
        let name = validate_mode_name(name)?.to_string();
        if self.modes.contains_key(&name) {
            return Err(AgentpackError::Mode(format!("mode already exists: {name}")));
        }
        self.modes.insert(
            name.clone(),
            ModeDefinition {
                base: ModeBase::All,
                ..Default::default()
            },
        );
        self.selected_mode = name;
        self.dirty = true;
        Ok(())
    }

    pub fn rename_selected_mode(&mut self, name: &str) -> Result<()> {
        if is_reserved_mode(&self.selected_mode) {
            return Err(AgentpackError::Mode(format!(
                "{DEFAULT_MODE_NAME} is reserved and cannot be renamed"
            )));
        }
        let name = validate_mode_name(name)?.to_string();
        if self.modes.contains_key(&name) {
            return Err(AgentpackError::Mode(format!("mode already exists: {name}")));
        }
        let definition = self
            .modes
            .remove(&self.selected_mode)
            .expect("selected mode should exist");
        self.modes.insert(name.clone(), definition);
        self.selected_mode = name;
        self.dirty = true;
        Ok(())
    }

    pub fn delete_selected_mode(&mut self) -> Result<()> {
        if is_reserved_mode(&self.selected_mode) {
            return Err(AgentpackError::Mode(format!(
                "{DEFAULT_MODE_NAME} is reserved and cannot be deleted"
            )));
        }
        self.modes.remove(&self.selected_mode);
        self.selected_mode = DEFAULT_MODE_NAME.into();
        self.dirty = true;
        Ok(())
    }

    pub fn set_base(&mut self, base: ModeBase) -> Result<()> {
        self.ensure_selected_editable()?;
        if let Some(mode) = self.modes.get_mut(&self.selected_mode) {
            if mode.base != base {
                mode.base = base;
                self.dirty = true;
            }
        }
        Ok(())
    }

    pub fn apply_selector(&mut self, raw: &str, enabled: bool) -> Result<()> {
        self.ensure_selected_editable()?;
        let selector = Selector::parse(raw)?.canonical_string();
        let mode = self
            .modes
            .get_mut(&self.selected_mode)
            .expect("selected mode should exist");
        if enabled {
            mode.disable.retain(|entry| entry != &selector);
            if !mode.enable.iter().any(|entry| entry == &selector) {
                mode.enable.push(selector);
            }
        } else {
            mode.enable.retain(|entry| entry != &selector);
            if !mode.disable.iter().any(|entry| entry == &selector) {
                mode.disable.push(selector);
            }
        }
        mode.sort_and_dedup();
        self.dirty = true;
        Ok(())
    }

    pub fn clear_selector(&mut self, raw: &str) -> Result<()> {
        self.ensure_selected_editable()?;
        let selector = Selector::parse(raw)?.canonical_string();
        let mode = self
            .modes
            .get_mut(&self.selected_mode)
            .expect("selected mode should exist");
        let before = mode.enable.len() + mode.disable.len();
        mode.enable.retain(|entry| entry != &selector);
        mode.disable.retain(|entry| entry != &selector);
        if mode.enable.len() + mode.disable.len() != before {
            self.dirty = true;
        }
        Ok(())
    }

    pub fn selected_is_read_only(&self) -> bool {
        is_reserved_mode(&self.selected_mode)
    }

    fn ensure_selected_editable(&self) -> Result<()> {
        if self.selected_is_read_only() {
            return Err(AgentpackError::Mode(format!(
                "{DEFAULT_MODE_NAME} is read-only"
            )));
        }
        Ok(())
    }

    pub fn selector_state(&self, canonical: &str) -> SelectorState {
        let mode = self.selected_definition();
        if mode.enable.iter().any(|entry| entry == canonical) {
            SelectorState::ExplicitEnable
        } else if mode.disable.iter().any(|entry| entry == canonical) {
            SelectorState::ExplicitDisable
        } else {
            SelectorState::Neutral
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SelectorState {
    Neutral,
    ExplicitEnable,
    ExplicitDisable,
}

#[cfg(test)]
mod reducer_tests {
    use super::*;

    fn empty_manifest() -> AgentpackManifest {
        AgentpackManifest {
            name: "proj".into(),
            version: "0.1.0".into(),
            description: String::new(),
            dependencies: Default::default(),
            modes: Default::default(),
            mcp: Default::default(),
        }
    }

    #[test]
    fn reducer_marks_state_dirty_and_prevents_default_delete() {
        let manifest = empty_manifest();
        let mut state = ModeEditorState::load(&manifest, None).unwrap();
        assert!(state.delete_selected_mode().is_err());
        state.create_mode("design").unwrap();
        state.apply_selector("mcp:filesystem", false).unwrap();
        assert!(state.dirty);
        assert_eq!(state.selected_mode, "design");
        assert_eq!(state.selected_definition().disable, vec!["mcp:filesystem"]);
    }

    #[test]
    fn apply_selector_moves_between_enable_and_disable_without_duplicates() {
        let manifest = empty_manifest();
        let mut state = ModeEditorState::load(&manifest, None).unwrap();
        state.create_mode("design").unwrap();
        state.apply_selector("mcp:filesystem", true).unwrap();
        state.apply_selector("mcp:filesystem", true).unwrap();
        assert_eq!(state.selected_definition().enable, vec!["mcp:filesystem"]);
        state.apply_selector("mcp:filesystem", false).unwrap();
        assert!(state.selected_definition().enable.is_empty());
        assert_eq!(state.selected_definition().disable, vec!["mcp:filesystem"]);
        state.clear_selector("mcp:filesystem").unwrap();
        assert!(state.selected_definition().enable.is_empty());
        assert!(state.selected_definition().disable.is_empty());
    }
}

// ---------------------------------------------------------------------------
// Capability tree
// ---------------------------------------------------------------------------

#[derive(Clone, Debug)]
struct TreeNode {
    id: String,
    label: String,
    /// Dimmed context shown after `label` (e.g. `owner/repo` for a package leaf).
    subtitle: Option<String>,
    selector: Option<Selector>,
    children: Vec<TreeNode>,
}

impl TreeNode {
    fn leaf(id: impl Into<String>, label: impl Into<String>, selector: Selector) -> Self {
        Self {
            id: id.into(),
            label: label.into(),
            subtitle: None,
            selector: Some(selector),
            children: Vec::new(),
        }
    }

    fn section(id: impl Into<String>, label: impl Into<String>) -> Self {
        Self {
            id: id.into(),
            label: label.into(),
            subtitle: None,
            selector: None,
            children: Vec::new(),
        }
    }
}

/// Splits a module id into a short, scannable leaf label and a dimmed parent
/// path. `github.com/anthropics/claude-plugins-official/plugins/code-review`
/// becomes `("code-review", Some("anthropics/claude-plugins-official/plugins"))`.
fn package_display(module: &str) -> (String, Option<String>) {
    if let Some(rest) = module.strip_prefix("github.com/") {
        let segments: Vec<&str> = rest.split('/').filter(|s| !s.is_empty()).collect();
        match segments.as_slice() {
            [] => (module.to_string(), None),
            [owner] => (module.to_string(), Some((*owner).into())),
            [owner, repo] => ((*repo).into(), Some((*owner).into())),
            [owner, repo, mid @ .., leaf] => {
                let parent = if mid.is_empty() {
                    format!("{owner}/{repo}")
                } else {
                    format!("{owner}/{repo}/{}", mid.join("/"))
                };
                ((*leaf).into(), Some(parent))
            }
        }
    } else {
        (module.to_string(), None)
    }
}

fn build_tree(catalog: &CapabilityCatalog) -> Vec<TreeNode> {
    let mut roots = Vec::new();

    let mut packages = TreeNode::section("section:packages", "Packages");
    for module in catalog.package_modules() {
        let (label, subtitle) = package_display(module);
        let mut node = TreeNode {
            id: format!("package:{module}"),
            label,
            subtitle,
            selector: Some(Selector::Package {
                module: module.clone(),
            }),
            children: Vec::new(),
        };
        if let Some(paths) = catalog.package_paths(module) {
            node.children = build_path_children(&format!("package-path:{module}:"), module, paths);
        }
        packages.children.push(node);
    }
    if !packages.children.is_empty() {
        roots.push(packages);
    }

    let mut mcp = TreeNode::section("section:mcp", "MCP servers");
    for name in catalog.mcp_names() {
        mcp.children.push(TreeNode::leaf(
            format!("mcp:{name}"),
            name.clone(),
            Selector::Mcp { name: name.clone() },
        ));
    }
    if !mcp.children.is_empty() {
        roots.push(mcp);
    }

    let mut dot_agents = TreeNode::section("section:.agents", ".agents");
    if !catalog.dot_agents_paths().is_empty() {
        dot_agents.children = build_dot_agents_children(catalog.dot_agents_paths());
    }
    if !dot_agents.children.is_empty() {
        roots.push(dot_agents);
    }

    roots
}

fn build_path_children(id_prefix: &str, module: &str, paths: &BTreeSet<String>) -> Vec<TreeNode> {
    let mut forest: Vec<TreeNode> = Vec::new();
    for rel in paths {
        let segments: Vec<&str> = rel.split('/').filter(|s| !s.is_empty()).collect();
        if segments.is_empty() {
            continue;
        }
        insert_path_node(&mut forest, &segments, 0, id_prefix, rel, |rel_path| {
            Selector::PackagePath {
                module: module.to_string(),
                rel_path: rel_path.to_string(),
            }
        });
    }
    forest
}

fn build_dot_agents_children(paths: &BTreeSet<String>) -> Vec<TreeNode> {
    let mut forest: Vec<TreeNode> = Vec::new();
    for rel in paths {
        let segments: Vec<&str> = rel.split('/').filter(|s| !s.is_empty()).collect();
        if segments.is_empty() {
            continue;
        }
        insert_path_node(&mut forest, &segments, 0, ".agents:", rel, |rel_path| {
            Selector::DotAgents {
                rel_path: rel_path.to_string(),
            }
        });
    }
    forest
}

/// Inserts a path into an existing forest, creating interior nodes as needed.
/// Each interior node receives a selector so "enable subtree" works at every
/// level.
fn insert_path_node(
    forest: &mut Vec<TreeNode>,
    segments: &[&str],
    depth: usize,
    id_prefix: &str,
    full_rel: &str,
    make_selector: impl Fn(&str) -> Selector + Copy,
) {
    if depth >= segments.len() {
        return;
    }
    let current_rel = segments[..=depth].join("/");
    let node_id = format!("{id_prefix}{current_rel}");
    let label = segments[depth].to_string();
    let pos = forest.iter().position(|node| node.id == node_id);
    let index = match pos {
        Some(index) => index,
        None => {
            forest.push(TreeNode {
                id: node_id,
                label,
                subtitle: None,
                selector: Some(make_selector(&current_rel)),
                children: Vec::new(),
            });
            forest.len() - 1
        }
    };
    if depth + 1 < segments.len() {
        insert_path_node(
            &mut forest[index].children,
            segments,
            depth + 1,
            id_prefix,
            full_rel,
            make_selector,
        );
    } else {
        // Leaf — already inserted above. No further children.
        debug_assert_eq!(forest[index].id, format!("{id_prefix}{full_rel}"));
    }
}

// ---------------------------------------------------------------------------
// TUI state
// ---------------------------------------------------------------------------

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum Focus {
    Modes,
    Tree,
}

#[derive(Clone, Debug)]
enum Prompt {
    CreateMode { buffer: String },
    RenameMode { buffer: String },
    AddSelector { buffer: String, enable: bool },
    ConfirmDelete { mode: String },
    ConfirmQuitDirty,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum MessageKind {
    Info,
    Error,
}

struct TuiApp {
    project_root: PathBuf,
    state: ModeEditorState,
    catalog: CapabilityCatalog,
    tree: Vec<TreeNode>,
    focus: Focus,
    modes_cursor: usize,
    tree_cursor: usize,
    expanded: BTreeSet<String>,
    prompt: Option<Prompt>,
    message: Option<(String, MessageKind)>,
    show_help: bool,
    quit: bool,
}

impl TuiApp {
    fn new(
        project_root: &Path,
        manifest: &AgentpackManifest,
        catalog: CapabilityCatalog,
        selected_mode: Option<&str>,
    ) -> Result<Self> {
        let state = ModeEditorState::load(manifest, selected_mode)?;
        let tree = build_tree(&catalog);
        let mut expanded = BTreeSet::new();
        // Expand section headers by default so the user sees something useful.
        for root in &tree {
            expanded.insert(root.id.clone());
        }
        let mode_names: Vec<String> = state.modes.keys().cloned().collect();
        let modes_cursor = mode_names
            .iter()
            .position(|name| name == &state.selected_mode)
            .unwrap_or(0);
        Ok(Self {
            project_root: project_root.to_path_buf(),
            state,
            catalog,
            tree,
            focus: Focus::Modes,
            modes_cursor,
            tree_cursor: 0,
            expanded,
            prompt: None,
            message: None,
            show_help: false,
            quit: false,
        })
    }

    fn mode_names(&self) -> Vec<String> {
        self.state.modes.keys().cloned().collect()
    }

    fn sync_mode_cursor(&mut self) {
        let names = self.mode_names();
        if let Some(idx) = names
            .iter()
            .position(|name| name == &self.state.selected_mode)
        {
            self.modes_cursor = idx;
        } else if !names.is_empty() {
            self.modes_cursor = 0;
            self.state.selected_mode = names[0].clone();
        }
    }

    fn flatten_tree(&self) -> Vec<VisibleRow<'_>> {
        let mut out = Vec::new();
        for root in &self.tree {
            collect_visible(root, 0, &self.expanded, &mut out);
        }
        out
    }

    fn effective_mode(&self) -> EffectiveMode {
        EffectiveMode::from_definition(
            &self.state.selected_mode,
            self.state.selected_definition().clone(),
        )
        .unwrap_or_else(|_| EffectiveMode::implicit_default())
    }

    fn info(&mut self, msg: impl Into<String>) {
        self.message = Some((msg.into(), MessageKind::Info));
    }

    fn error(&mut self, msg: impl Into<String>) {
        self.message = Some((msg.into(), MessageKind::Error));
    }

    fn save(&mut self) {
        match AgentpackManifest::replace_modes(&self.project_root, &self.state.modes) {
            Ok(()) => {
                self.state.dirty = false;
                self.info("Saved.");
            }
            Err(error) => self.error(format!("Save failed: {error}")),
        }
    }
}

struct VisibleRow<'a> {
    node: &'a TreeNode,
    depth: usize,
    expandable: bool,
    expanded: bool,
}

fn collect_visible<'a>(
    node: &'a TreeNode,
    depth: usize,
    expanded: &BTreeSet<String>,
    out: &mut Vec<VisibleRow<'a>>,
) {
    let is_expanded = expanded.contains(&node.id);
    out.push(VisibleRow {
        node,
        depth,
        expandable: !node.children.is_empty(),
        expanded: is_expanded,
    });
    if is_expanded {
        for child in &node.children {
            collect_visible(child, depth + 1, expanded, out);
        }
    }
}

// ---------------------------------------------------------------------------
// Event handling
// ---------------------------------------------------------------------------

fn handle_event(app: &mut TuiApp, event: Event) {
    let key = match event {
        Event::Key(key) if key.kind == event::KeyEventKind::Press => key,
        _ => return,
    };

    if app.prompt.is_some() {
        handle_prompt_key(app, key.code, key.modifiers);
        return;
    }

    if key.modifiers.contains(KeyModifiers::CONTROL) && matches!(key.code, KeyCode::Char('c')) {
        request_quit(app);
        return;
    }

    app.message = None;

    match key.code {
        KeyCode::Char('q') => request_quit(app),
        KeyCode::Char('s') => app.save(),
        KeyCode::Char('?') | KeyCode::F(1) => app.show_help = !app.show_help,
        KeyCode::Tab => {
            app.focus = match app.focus {
                Focus::Modes => Focus::Tree,
                Focus::Tree => Focus::Modes,
            };
        }
        _ => match app.focus {
            Focus::Modes => handle_modes_key(app, key.code),
            Focus::Tree => handle_tree_key(app, key.code),
        },
    }
}

fn request_quit(app: &mut TuiApp) {
    if app.state.dirty {
        app.prompt = Some(Prompt::ConfirmQuitDirty);
    } else {
        app.quit = true;
    }
}

fn handle_modes_key(app: &mut TuiApp, code: KeyCode) {
    let names = app.mode_names();
    match code {
        KeyCode::Up | KeyCode::Char('k') => {
            if app.modes_cursor > 0 {
                app.modes_cursor -= 1;
                app.state.selected_mode = names[app.modes_cursor].clone();
            }
        }
        KeyCode::Down | KeyCode::Char('j') => {
            if app.modes_cursor + 1 < names.len() {
                app.modes_cursor += 1;
                app.state.selected_mode = names[app.modes_cursor].clone();
            }
        }
        KeyCode::Enter | KeyCode::Right => app.focus = Focus::Tree,
        KeyCode::Char('n') => {
            app.prompt = Some(Prompt::CreateMode {
                buffer: String::new(),
            });
        }
        KeyCode::Char('r') => {
            if is_reserved_mode(&app.state.selected_mode) {
                app.error(format!(
                    "{DEFAULT_MODE_NAME} is reserved and cannot be renamed"
                ));
            } else {
                app.prompt = Some(Prompt::RenameMode {
                    buffer: app.state.selected_mode.clone(),
                });
            }
        }
        KeyCode::Char('d') | KeyCode::Delete => {
            if is_reserved_mode(&app.state.selected_mode) {
                app.error(format!(
                    "{DEFAULT_MODE_NAME} is reserved and cannot be deleted"
                ));
            } else {
                app.prompt = Some(Prompt::ConfirmDelete {
                    mode: app.state.selected_mode.clone(),
                });
            }
        }
        KeyCode::Char('b') => {
            let next = match app.state.selected_definition().base {
                ModeBase::All => ModeBase::None,
                ModeBase::None => ModeBase::All,
            };
            match app.state.set_base(next) {
                Ok(()) => app.info(format!("base = {next}")),
                Err(error) => app.error(error.to_string()),
            }
        }
        _ => {}
    }
}

fn handle_tree_key(app: &mut TuiApp, code: KeyCode) {
    let snapshot: Vec<(String, bool, bool)> = app
        .flatten_tree()
        .iter()
        .map(|row| (row.node.id.clone(), row.expandable, row.expanded))
        .collect();
    if snapshot.is_empty() {
        return;
    }
    if app.tree_cursor >= snapshot.len() {
        app.tree_cursor = snapshot.len() - 1;
    }

    match code {
        KeyCode::Up | KeyCode::Char('k') => {
            if app.tree_cursor > 0 {
                app.tree_cursor -= 1;
            }
        }
        KeyCode::Down | KeyCode::Char('j') => {
            if app.tree_cursor + 1 < snapshot.len() {
                app.tree_cursor += 1;
            }
        }
        KeyCode::Left | KeyCode::Char('h') => {
            let (id, expandable, expanded) = &snapshot[app.tree_cursor];
            if *expandable && *expanded {
                app.expanded.remove(id);
            } else {
                app.focus = Focus::Modes;
            }
        }
        KeyCode::Right | KeyCode::Char('l') => {
            let (id, expandable, expanded) = &snapshot[app.tree_cursor];
            if *expandable && !*expanded {
                app.expanded.insert(id.clone());
            }
        }
        KeyCode::Enter | KeyCode::Char(' ') => {
            let (id, expandable, expanded) = &snapshot[app.tree_cursor];
            if *expandable {
                if *expanded {
                    app.expanded.remove(id);
                } else {
                    app.expanded.insert(id.clone());
                }
            }
        }
        KeyCode::Char('e') => {
            apply_cursor_selector(app, Some(true));
        }
        KeyCode::Char('x') => {
            apply_cursor_selector(app, Some(false));
        }
        KeyCode::Char('c') => {
            clear_cursor_selector(app);
        }
        KeyCode::Char('t') => {
            cycle_cursor_selector(app);
        }
        KeyCode::Char('E') => {
            apply_cursor_subtree(app, true);
        }
        KeyCode::Char('X') => {
            apply_cursor_subtree(app, false);
        }
        KeyCode::Char('a') => {
            if app.state.selected_is_read_only() {
                app.error(format!("{DEFAULT_MODE_NAME} is read-only"));
            } else {
                app.prompt = Some(Prompt::AddSelector {
                    buffer: String::new(),
                    enable: true,
                });
            }
        }
        KeyCode::Char('A') => {
            if app.state.selected_is_read_only() {
                app.error(format!("{DEFAULT_MODE_NAME} is read-only"));
            } else {
                app.prompt = Some(Prompt::AddSelector {
                    buffer: String::new(),
                    enable: false,
                });
            }
        }
        _ => {}
    }
}

fn cursor_selector(app: &TuiApp) -> Option<Selector> {
    let visible = app.flatten_tree();
    visible
        .get(app.tree_cursor)
        .and_then(|row| row.node.selector.clone())
}

fn cursor_node_id(app: &TuiApp) -> Option<String> {
    let visible = app.flatten_tree();
    visible.get(app.tree_cursor).map(|row| row.node.id.clone())
}

fn apply_cursor_selector(app: &mut TuiApp, enable: Option<bool>) {
    let Some(selector) = cursor_selector(app) else {
        app.error("nothing selectable here");
        return;
    };
    let canonical = selector.canonical_string();
    let result = match enable {
        Some(true) => app.state.apply_selector(&canonical, true),
        Some(false) => app.state.apply_selector(&canonical, false),
        None => app.state.clear_selector(&canonical),
    };
    if let Err(error) = result {
        app.error(error.to_string());
    } else {
        app.info(match enable {
            Some(true) => format!("enable {canonical}"),
            Some(false) => format!("disable {canonical}"),
            None => format!("cleared {canonical}"),
        });
    }
}

fn clear_cursor_selector(app: &mut TuiApp) {
    apply_cursor_selector(app, None);
}

fn cycle_cursor_selector(app: &mut TuiApp) {
    let Some(selector) = cursor_selector(app) else {
        app.error("nothing selectable here");
        return;
    };
    let canonical = selector.canonical_string();
    let state = app.state.selector_state(&canonical);
    let result = match state {
        SelectorState::Neutral => app.state.apply_selector(&canonical, true),
        SelectorState::ExplicitEnable => app.state.apply_selector(&canonical, false),
        SelectorState::ExplicitDisable => app.state.clear_selector(&canonical),
    };
    if let Err(error) = result {
        app.error(error.to_string());
    }
}

/// Force every selector under the cursor node into a uniform state.
///
/// Because the selector specificity model guarantees that a broader selector
/// matches any descendant, we only need to (a) clear every explicit enable /
/// disable under the subtree (so no more-specific override remains) and
/// (b) apply the cursor node's own selector in the target state. Emitting the
/// descendants too would be correct but bloats `agentpack.toml` with redundant
/// entries.
fn apply_cursor_subtree(app: &mut TuiApp, enable: bool) {
    if app.state.selected_is_read_only() {
        app.error(format!("{DEFAULT_MODE_NAME} is read-only"));
        return;
    }
    let Some(id) = cursor_node_id(app) else {
        return;
    };
    let Some(node) = find_node(&app.tree, &id) else {
        return;
    };
    let mut descendants = Vec::new();
    collect_subtree_selectors(node, &mut descendants);
    let Some(root_selector) = node.selector.as_ref().map(Selector::canonical_string) else {
        app.error("nothing selectable here");
        return;
    };

    for canonical in &descendants {
        if canonical != &root_selector {
            let _ = app.state.clear_selector(canonical);
        }
    }
    if let Err(error) = app.state.apply_selector(&root_selector, enable) {
        app.error(error.to_string());
        return;
    }
    app.info(format!(
        "{} {root_selector}",
        if enable { "enabled" } else { "disabled" }
    ));
}

fn find_node<'a>(nodes: &'a [TreeNode], id: &str) -> Option<&'a TreeNode> {
    for node in nodes {
        if node.id == id {
            return Some(node);
        }
        if let Some(found) = find_node(&node.children, id) {
            return Some(found);
        }
    }
    None
}

fn collect_subtree_selectors(node: &TreeNode, out: &mut Vec<String>) {
    if let Some(selector) = &node.selector {
        out.push(selector.canonical_string());
    }
    for child in &node.children {
        collect_subtree_selectors(child, out);
    }
}

fn handle_prompt_key(app: &mut TuiApp, code: KeyCode, modifiers: KeyModifiers) {
    let Some(prompt) = app.prompt.clone() else {
        return;
    };
    match prompt {
        Prompt::ConfirmDelete { mode } => match code {
            KeyCode::Char('y') | KeyCode::Char('Y') => {
                app.prompt = None;
                app.state.selected_mode = mode.clone();
                match app.state.delete_selected_mode() {
                    Ok(()) => {
                        app.sync_mode_cursor();
                        app.info(format!("deleted mode {mode}"));
                    }
                    Err(error) => app.error(error.to_string()),
                }
            }
            KeyCode::Esc | KeyCode::Char('n') | KeyCode::Char('N') => app.prompt = None,
            _ => {}
        },
        Prompt::ConfirmQuitDirty => match code {
            KeyCode::Char('y') | KeyCode::Char('Y') => {
                app.save();
                if !app.state.dirty {
                    app.quit = true;
                }
                app.prompt = None;
            }
            KeyCode::Char('n') | KeyCode::Char('N') => {
                app.quit = true;
                app.prompt = None;
            }
            KeyCode::Esc => app.prompt = None,
            _ => {}
        },
        Prompt::CreateMode { mut buffer } => match text_input_key(code, modifiers, &mut buffer) {
            TextInputAction::Cancel => app.prompt = None,
            TextInputAction::Submit => match app.state.create_mode(buffer.trim()) {
                Ok(()) => {
                    app.sync_mode_cursor();
                    app.info(format!("created mode {}", buffer.trim()));
                    app.prompt = None;
                }
                Err(error) => {
                    app.error(error.to_string());
                    app.prompt = Some(Prompt::CreateMode { buffer });
                }
            },
            TextInputAction::Continue => {
                app.prompt = Some(Prompt::CreateMode { buffer });
            }
        },
        Prompt::RenameMode { mut buffer } => match text_input_key(code, modifiers, &mut buffer) {
            TextInputAction::Cancel => app.prompt = None,
            TextInputAction::Submit => match app.state.rename_selected_mode(buffer.trim()) {
                Ok(()) => {
                    app.sync_mode_cursor();
                    app.info(format!("renamed to {}", buffer.trim()));
                    app.prompt = None;
                }
                Err(error) => {
                    app.error(error.to_string());
                    app.prompt = Some(Prompt::RenameMode { buffer });
                }
            },
            TextInputAction::Continue => {
                app.prompt = Some(Prompt::RenameMode { buffer });
            }
        },
        Prompt::AddSelector { mut buffer, enable } => {
            match text_input_key(code, modifiers, &mut buffer) {
                TextInputAction::Cancel => app.prompt = None,
                TextInputAction::Submit => match submit_selector(app, buffer.trim(), enable) {
                    Ok(canonical) => {
                        app.info(format!(
                            "{} {canonical}",
                            if enable { "enable" } else { "disable" }
                        ));
                        app.prompt = None;
                    }
                    Err(error) => {
                        app.error(error.to_string());
                        app.prompt = Some(Prompt::AddSelector { buffer, enable });
                    }
                },
                TextInputAction::Continue => {
                    app.prompt = Some(Prompt::AddSelector { buffer, enable });
                }
            }
        }
    }
}

enum TextInputAction {
    Cancel,
    Submit,
    Continue,
}

fn text_input_key(code: KeyCode, modifiers: KeyModifiers, buffer: &mut String) -> TextInputAction {
    match code {
        KeyCode::Esc => TextInputAction::Cancel,
        KeyCode::Enter => TextInputAction::Submit,
        KeyCode::Backspace => {
            buffer.pop();
            TextInputAction::Continue
        }
        KeyCode::Char(c) if !modifiers.contains(KeyModifiers::CONTROL) => {
            buffer.push(c);
            TextInputAction::Continue
        }
        _ => TextInputAction::Continue,
    }
}

fn submit_selector(app: &mut TuiApp, raw: &str, enable: bool) -> Result<String> {
    let selector = Selector::parse(raw)?;
    app.catalog.validate_selector(&selector)?;
    let canonical = selector.canonical_string();
    app.state.apply_selector(&canonical, enable)?;
    Ok(canonical)
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

fn render(frame: &mut ratatui::Frame, app: &TuiApp) {
    let area = frame.area();
    let outer = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(1),
            Constraint::Min(3),
            Constraint::Length(3),
        ])
        .split(area);

    render_title(frame, outer[0], app);
    render_body(frame, outer[1], app);
    render_footer(frame, outer[2], app);

    if let Some(prompt) = &app.prompt {
        render_prompt(frame, area, prompt);
    } else if app.show_help {
        render_help(frame, area);
    }
}

fn render_title(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let dirty = if app.state.dirty { " *modified*" } else { "" };
    let title = format!(
        " agentpack mode tui  —  project: {}{}",
        app.project_root.display(),
        dirty
    );
    frame.render_widget(
        Paragraph::new(title).style(Style::default().add_modifier(Modifier::BOLD)),
        area,
    );
}

fn render_body(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let columns = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([
            Constraint::Percentage(22),
            Constraint::Percentage(48),
            Constraint::Percentage(30),
        ])
        .split(area);

    render_modes(frame, columns[0], app);
    render_tree(frame, columns[1], app);
    render_details(frame, columns[2], app);
}

fn render_modes(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let names = app.mode_names();
    let items: Vec<ListItem> = names
        .iter()
        .map(|name| {
            let mut spans = Vec::new();
            if is_reserved_mode(name) {
                spans.push(Span::styled("● ", Style::default().fg(Color::Yellow)));
            } else {
                spans.push(Span::raw("  "));
            }
            spans.push(Span::raw(name.clone()));
            if is_reserved_mode(name) {
                spans.push(Span::styled(
                    "  (read-only)",
                    Style::default().fg(Color::DarkGray),
                ));
            }
            ListItem::new(Line::from(spans))
        })
        .collect();
    let mut list_state = ListState::default();
    list_state.select(Some(app.modes_cursor));
    let block = Block::default()
        .borders(Borders::ALL)
        .title(focus_title(" Modes ", app.focus == Focus::Modes));
    let list = List::new(items)
        .block(block)
        .highlight_style(selection_style(app.focus == Focus::Modes))
        .highlight_symbol(" ▶ ");
    frame.render_stateful_widget(list, area, &mut list_state);
}

fn render_tree(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let visible = app.flatten_tree();
    let effective = app.effective_mode();
    let items: Vec<ListItem> = visible
        .iter()
        .map(|row| ListItem::new(tree_row_line(row, &app.state, &effective)))
        .collect();

    let mut list_state = ListState::default();
    if !visible.is_empty() {
        list_state.select(Some(app.tree_cursor.min(visible.len() - 1)));
    }

    let block = Block::default()
        .borders(Borders::ALL)
        .title(focus_title(" Capability tree ", app.focus == Focus::Tree));
    let list = List::new(items)
        .block(block)
        .highlight_style(selection_style(app.focus == Focus::Tree))
        .highlight_symbol(" ▶ ");
    frame.render_stateful_widget(list, area, &mut list_state);
}

/// Selection background that stays dark enough for the underlying span colors
/// (yellow reserved-mode bullet, default text, dimmed subtitles) to remain
/// readable. Avoids ratatui's bright `Color::Blue`, which renders close to a
/// light cyan on many terminals and washes out the highlighted row.
fn selection_style(focused: bool) -> Style {
    let bg = if focused {
        Color::Rgb(38, 70, 120)
    } else {
        Color::Rgb(48, 48, 48)
    };
    Style::default().bg(bg).add_modifier(Modifier::BOLD)
}

fn tree_row_line<'a>(
    row: &VisibleRow<'a>,
    state: &ModeEditorState,
    effective: &EffectiveMode,
) -> Line<'a> {
    let indent = "  ".repeat(row.depth);
    let caret = if row.expandable {
        if row.expanded {
            "▼ "
        } else {
            "▶ "
        }
    } else {
        "• "
    };
    let (glyph, glyph_style) = row_glyph(row.node, state, effective);
    let label_style = if row.node.selector.is_none() {
        Style::default().add_modifier(Modifier::BOLD)
    } else {
        Style::default()
    };
    let mut spans = vec![
        Span::raw(indent),
        Span::raw(caret),
        Span::styled(glyph, glyph_style),
        Span::raw(" "),
        Span::styled(row.node.label.clone(), label_style),
    ];
    if let Some(subtitle) = &row.node.subtitle {
        spans.push(Span::styled(
            format!("  {subtitle}"),
            Style::default().fg(Color::DarkGray),
        ));
    }
    Line::from(spans)
}

fn row_glyph(
    node: &TreeNode,
    state: &ModeEditorState,
    effective: &EffectiveMode,
) -> (&'static str, Style) {
    let Some(selector) = &node.selector else {
        return ("[ ]", Style::default().fg(Color::DarkGray));
    };
    let canonical = selector.canonical_string();
    let explicit = state.selector_state(&canonical);
    let allowed = selector_is_allowed(effective, selector);
    let base_color = match allowed {
        Some(true) => Color::Green,
        Some(false) => Color::Red,
        None => Color::DarkGray,
    };
    let glyph = match (explicit, allowed) {
        (SelectorState::ExplicitEnable, _) => "[+]",
        (SelectorState::ExplicitDisable, _) => "[-]",
        (SelectorState::Neutral, Some(true)) => "[✓]",
        (SelectorState::Neutral, Some(false)) => "[✗]",
        (SelectorState::Neutral, None) => "[ ]",
    };
    (
        glyph,
        Style::default().fg(base_color).add_modifier(Modifier::BOLD),
    )
}

fn selector_is_allowed(effective: &EffectiveMode, selector: &Selector) -> Option<bool> {
    match selector {
        Selector::Package { module } => effective.allows_package_path(module, Path::new("")).ok(),
        Selector::PackagePath { module, rel_path } => effective
            .allows_package_path(module, Path::new(rel_path))
            .ok(),
        Selector::Mcp { name } => Some(effective.allows_mcp(name)),
        Selector::DotAgents { rel_path } => {
            effective.allows_dot_agents_path(Path::new(rel_path)).ok()
        }
    }
}

fn render_details(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let mut lines: Vec<Line> = Vec::new();
    let definition = app.state.selected_definition();
    let mut mode_spans = vec![
        Span::styled("Mode: ", Style::default().add_modifier(Modifier::BOLD)),
        Span::raw(app.state.selected_mode.clone()),
    ];
    if app.state.selected_is_read_only() {
        mode_spans.push(Span::styled(
            "  (read-only)",
            Style::default().fg(Color::DarkGray),
        ));
    }
    lines.push(Line::from(mode_spans));
    lines.push(Line::from(vec![
        Span::styled("Base: ", Style::default().add_modifier(Modifier::BOLD)),
        Span::styled(
            definition.base.to_string(),
            Style::default().fg(match definition.base {
                ModeBase::All => Color::Green,
                ModeBase::None => Color::Red,
            }),
        ),
    ]));
    lines.push(Line::from(""));

    let visible = app.flatten_tree();
    if let Some(row) = visible.get(app.tree_cursor) {
        lines.push(Line::from(vec![Span::styled(
            "Cursor",
            Style::default().add_modifier(Modifier::BOLD),
        )]));
        lines.push(Line::from(format!("label: {}", row.node.label)));
        if let Some(selector) = &row.node.selector {
            lines.push(Line::from(format!(
                "selector: {}",
                selector.canonical_string()
            )));
        } else {
            lines.push(Line::from("selector: (section header)"));
        }
        lines.push(Line::from(""));
    }

    lines.push(Line::from(vec![Span::styled(
        format!("Enable ({})", definition.enable.len()),
        Style::default().add_modifier(Modifier::BOLD),
    )]));
    if definition.enable.is_empty() {
        lines.push(Line::from(Span::styled(
            "  (none)",
            Style::default().fg(Color::DarkGray),
        )));
    } else {
        for entry in &definition.enable {
            lines.push(Line::from(format!("  + {entry}")));
        }
    }
    lines.push(Line::from(""));
    lines.push(Line::from(vec![Span::styled(
        format!("Disable ({})", definition.disable.len()),
        Style::default().add_modifier(Modifier::BOLD),
    )]));
    if definition.disable.is_empty() {
        lines.push(Line::from(Span::styled(
            "  (none)",
            Style::default().fg(Color::DarkGray),
        )));
    } else {
        for entry in &definition.disable {
            lines.push(Line::from(format!("  - {entry}")));
        }
    }

    let paragraph = Paragraph::new(lines)
        .wrap(Wrap { trim: false })
        .block(Block::default().borders(Borders::ALL).title(" Details "));
    frame.render_widget(paragraph, area);
}

fn render_footer(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let layout = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(1), Constraint::Length(2)])
        .split(area);

    let message = match &app.message {
        Some((text, MessageKind::Info)) => Line::from(Span::styled(
            text.clone(),
            Style::default().fg(Color::LightGreen),
        )),
        Some((text, MessageKind::Error)) => Line::from(Span::styled(
            text.clone(),
            Style::default().fg(Color::LightRed),
        )),
        None => Line::from(""),
    };
    frame.render_widget(Paragraph::new(message), layout[0]);

    let hints = match app.focus {
        Focus::Modes => {
            "[↑/↓] select  [n]ew  [r]ename  [d]elete  [b]ase all/none  [Tab] tree  [s]ave  [?] help  [q]uit"
        }
        Focus::Tree => {
            "[↑/↓] move  [←/→] fold  [t] cycle  [e] enable  [x] disable  [c] clear  [E/X] subtree  [a/A] add  [Tab] modes  [s]ave  [q]uit"
        }
    };
    frame.render_widget(
        Paragraph::new(Line::from(Span::styled(
            hints,
            Style::default().fg(Color::Gray),
        )))
        .block(Block::default().borders(Borders::TOP)),
        layout[1],
    );
}

fn focus_title(text: &str, focused: bool) -> Span<'_> {
    if focused {
        Span::styled(
            text,
            Style::default()
                .fg(Color::LightCyan)
                .add_modifier(Modifier::BOLD),
        )
    } else {
        Span::styled(text, Style::default().fg(Color::Gray))
    }
}

fn render_prompt(frame: &mut ratatui::Frame, area: Rect, prompt: &Prompt) {
    let modal = centered_rect(60, 20, area);
    frame.render_widget(ratatui::widgets::Clear, modal);
    let (title, body) = match prompt {
        Prompt::CreateMode { buffer } => (
            " Create mode ",
            vec![
                Line::from("Enter a name for the new mode."),
                Line::from(""),
                Line::from(vec![
                    Span::raw("> "),
                    Span::styled(buffer.clone(), Style::default().fg(Color::LightCyan)),
                    Span::styled("_", Style::default().add_modifier(Modifier::SLOW_BLINK)),
                ]),
                Line::from(""),
                Line::from(Span::styled(
                    "[Enter] confirm  [Esc] cancel",
                    Style::default().fg(Color::Gray),
                )),
            ],
        ),
        Prompt::RenameMode { buffer } => (
            " Rename mode ",
            vec![
                Line::from("Enter the new mode name."),
                Line::from(""),
                Line::from(vec![
                    Span::raw("> "),
                    Span::styled(buffer.clone(), Style::default().fg(Color::LightCyan)),
                    Span::styled("_", Style::default().add_modifier(Modifier::SLOW_BLINK)),
                ]),
                Line::from(""),
                Line::from(Span::styled(
                    "[Enter] confirm  [Esc] cancel",
                    Style::default().fg(Color::Gray),
                )),
            ],
        ),
        Prompt::AddSelector { buffer, enable } => (
            if *enable {
                " Enable selector "
            } else {
                " Disable selector "
            },
            vec![
                Line::from(
                    "Type a selector: package:<mod> | package-path:<mod>:<rel> | mcp:<name> | .agents:<rel>",
                ),
                Line::from(""),
                Line::from(vec![
                    Span::raw("> "),
                    Span::styled(buffer.clone(), Style::default().fg(Color::LightCyan)),
                    Span::styled("_", Style::default().add_modifier(Modifier::SLOW_BLINK)),
                ]),
                Line::from(""),
                Line::from(Span::styled(
                    "[Enter] confirm  [Esc] cancel",
                    Style::default().fg(Color::Gray),
                )),
            ],
        ),
        Prompt::ConfirmDelete { mode } => (
            " Delete mode ",
            vec![
                Line::from(format!("Delete mode \"{mode}\"? This cannot be undone.")),
                Line::from(""),
                Line::from(Span::styled(
                    "[y] confirm  [n/Esc] cancel",
                    Style::default().fg(Color::Gray),
                )),
            ],
        ),
        Prompt::ConfirmQuitDirty => (
            " Unsaved changes ",
            vec![
                Line::from("You have unsaved changes. Save before exit?"),
                Line::from(""),
                Line::from(Span::styled(
                    "[y] save & quit  [n] discard & quit  [Esc] cancel",
                    Style::default().fg(Color::Gray),
                )),
            ],
        ),
    };
    let paragraph = Paragraph::new(body)
        .wrap(Wrap { trim: false })
        .alignment(Alignment::Left)
        .block(
            Block::default().borders(Borders::ALL).title(Span::styled(
                title,
                Style::default()
                    .fg(Color::LightYellow)
                    .add_modifier(Modifier::BOLD),
            )),
        );
    frame.render_widget(paragraph, modal);
}

fn render_help(frame: &mut ratatui::Frame, area: Rect) {
    let modal = centered_rect(60, 60, area);
    frame.render_widget(ratatui::widgets::Clear, modal);
    let help = vec![
        Line::from(Span::styled(
            " agentpack mode tui ",
            Style::default()
                .fg(Color::LightYellow)
                .add_modifier(Modifier::BOLD),
        )),
        Line::from(""),
        Line::from("Global:"),
        Line::from("  Tab       switch focus between Modes and Capability tree"),
        Line::from("  s         save modes to agentpack.toml"),
        Line::from("  q, Ctrl-C quit (prompts if unsaved)"),
        Line::from("  ?, F1     toggle this help"),
        Line::from(""),
        Line::from("Modes pane:"),
        Line::from("  ↑/↓ k/j   move selection"),
        Line::from("  Enter/→   focus capability tree"),
        Line::from("  n         new mode"),
        Line::from("  r         rename (default is reserved)"),
        Line::from("  d / Del   delete (default is reserved)"),
        Line::from("  b         toggle base between all and none"),
        Line::from(""),
        Line::from("Capability tree pane:"),
        Line::from("  ↑/↓ k/j   move cursor"),
        Line::from("  ←/→ h/l   fold / unfold"),
        Line::from("  Enter/Sp  toggle fold at cursor"),
        Line::from("  t         cycle: neutral → enable → disable → neutral"),
        Line::from("  e         force-enable selector at cursor"),
        Line::from("  x         force-disable selector at cursor"),
        Line::from("  c         clear selector at cursor"),
        Line::from("  E         enable every selector under cursor (subtree)"),
        Line::from("  X         disable every selector under cursor (subtree)"),
        Line::from("  a         prompt to add an enable selector"),
        Line::from("  A         prompt to add a disable selector"),
        Line::from(""),
        Line::from(
            "Selectors: package:<mod> · package-path:<mod>:<rel> · mcp:<name> · .agents:<rel>",
        ),
    ];
    let paragraph = Paragraph::new(help)
        .wrap(Wrap { trim: false })
        .block(Block::default().borders(Borders::ALL).title(" Help "));
    frame.render_widget(paragraph, modal);
}

fn centered_rect(percent_x: u16, percent_y: u16, area: Rect) -> Rect {
    let vertical = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Percentage((100 - percent_y) / 2),
            Constraint::Percentage(percent_y),
            Constraint::Percentage((100 - percent_y) / 2),
        ])
        .split(area);
    Layout::default()
        .direction(Direction::Horizontal)
        .constraints([
            Constraint::Percentage((100 - percent_x) / 2),
            Constraint::Percentage(percent_x),
            Constraint::Percentage((100 - percent_x) / 2),
        ])
        .split(vertical[1])[1]
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

pub fn run_mode_tui(
    project_root: &Path,
    manifest: &AgentpackManifest,
    catalog: &CapabilityCatalog,
    selected_mode: Option<&str>,
) -> Result<()> {
    let mut app = TuiApp::new(project_root, manifest, catalog.clone(), selected_mode)?;

    let mut terminal = enter_tui(project_root)?;
    let run_result = run_event_loop(&mut terminal, &mut app, project_root);
    leave_tui(&mut terminal);
    run_result
}

fn enter_tui(project_root: &Path) -> Result<Terminal<CrosstermBackend<io::Stdout>>> {
    enable_raw_mode().map_err(|error| AgentpackError::io(project_root, error))?;
    let mut stdout = io::stdout();
    if let Err(error) = execute!(stdout, EnterAlternateScreen, EnableMouseCapture) {
        disable_raw_mode().ok();
        return Err(AgentpackError::io(project_root, error));
    }
    Terminal::new(CrosstermBackend::new(stdout)).map_err(|error| {
        // Unwind the raw mode / alt-screen state so the user's terminal is usable.
        execute!(io::stdout(), LeaveAlternateScreen, DisableMouseCapture).ok();
        disable_raw_mode().ok();
        AgentpackError::io(project_root, error)
    })
}

fn leave_tui(terminal: &mut Terminal<CrosstermBackend<io::Stdout>>) {
    disable_raw_mode().ok();
    execute!(
        terminal.backend_mut(),
        LeaveAlternateScreen,
        DisableMouseCapture
    )
    .ok();
    terminal.show_cursor().ok();
}

fn run_event_loop(
    terminal: &mut Terminal<CrosstermBackend<io::Stdout>>,
    app: &mut TuiApp,
    project_root: &Path,
) -> Result<()> {
    loop {
        terminal
            .draw(|frame| render(frame, app))
            .map_err(|error| AgentpackError::io(project_root, error))?;
        if app.quit {
            break;
        }
        let event = event::read().map_err(|error| AgentpackError::io(project_root, error))?;
        handle_event(app, event);
    }
    Ok(())
}

#[cfg(test)]
mod tree_tests {
    use super::*;
    use crate::lockfile::{LockPackage, PackLock, PackageKind};
    use crate::paths::project_dot_agents_dir;
    use serial_test::serial;
    use std::fs;
    use tempfile::tempdir;

    #[test]
    #[serial]
    fn tree_contains_sections_and_leaves() {
        let dir = tempdir().unwrap();
        let root = dir.path();
        let cache_root = root.join("cache");
        fs::create_dir_all(cache_root.join("k".repeat(64)).join("hooks")).unwrap();
        std::env::set_var("AGENTPACK_HOME", root);
        fs::write(
            cache_root
                .join("k".repeat(64))
                .join("hooks")
                .join("hooks.json"),
            "{}",
        )
        .unwrap();
        fs::create_dir_all(project_dot_agents_dir(root).join("rules")).unwrap();
        fs::write(project_dot_agents_dir(root).join("rules").join("a.mdc"), "").unwrap();
        let lock = PackLock {
            lockfile_version: 2,
            packages: vec![LockPackage {
                module: "github.com/acme/repo".into(),
                direct: true,
                kind: PackageKind::Plugin,
                url: String::new(),
                owner: "acme".into(),
                repo: "repo".into(),
                path: String::new(),
                commit: "c".repeat(40),
                cache_key: "k".repeat(64),
                name: String::new(),
            }],
            ..Default::default()
        };

        let catalog = CapabilityCatalog::build(root, Some(&lock), None).unwrap();
        let tree = build_tree(&catalog);
        let section_ids: Vec<_> = tree.iter().map(|n| n.id.clone()).collect();
        assert!(section_ids.contains(&"section:packages".into()));
        assert!(section_ids.contains(&"section:.agents".into()));

        let mut collected = Vec::new();
        for root in &tree {
            walk(root, &mut collected);
        }
        assert!(collected
            .iter()
            .any(|id| id == "package-path:github.com/acme/repo:hooks/hooks.json"));
        assert!(collected.iter().any(|id| id == ".agents:rules/a.mdc"));
    }

    fn walk(node: &TreeNode, out: &mut Vec<String>) {
        out.push(node.id.clone());
        for child in &node.children {
            walk(child, out);
        }
    }
}
