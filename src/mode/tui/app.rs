//! TUI application state shared by the event handlers and renderers.

use std::collections::BTreeSet;
use std::path::{Path, PathBuf};

use crate::error::Result;
use crate::manifest::AgentpackManifest;

use crate::mode::catalog::CapabilityCatalog;
use crate::mode::filter::EffectiveMode;

use super::state::ModeEditorState;
use super::tree::{build_tree, TreeNode};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Focus {
    Modes,
    Tree,
}

#[derive(Clone, Debug)]
pub enum Prompt {
    CreateMode { buffer: String },
    RenameMode { buffer: String },
    AddSelector { buffer: String, enable: bool },
    ConfirmDelete { mode: String },
    ConfirmQuitDirty,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum MessageKind {
    Info,
    Error,
}

pub struct TuiApp {
    pub project_root: PathBuf,
    pub state: ModeEditorState,
    pub catalog: CapabilityCatalog,
    pub tree: Vec<TreeNode>,
    pub focus: Focus,
    pub modes_cursor: usize,
    pub tree_cursor: usize,
    pub expanded: BTreeSet<String>,
    pub prompt: Option<Prompt>,
    pub message: Option<(String, MessageKind)>,
    pub show_help: bool,
    pub quit: bool,
}

impl TuiApp {
    pub fn new(
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

    pub fn mode_names(&self) -> Vec<String> {
        self.state.modes.keys().cloned().collect()
    }

    pub fn sync_mode_cursor(&mut self) {
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

    pub fn flatten_tree(&self) -> Vec<VisibleRow<'_>> {
        let mut out = Vec::new();
        for root in &self.tree {
            collect_visible(root, 0, &self.expanded, &mut out);
        }
        out
    }

    pub fn effective_mode(&self) -> EffectiveMode {
        EffectiveMode::from_definition(
            &self.state.selected_mode,
            self.state.selected_definition().clone(),
        )
        .unwrap_or_else(|_| EffectiveMode::implicit_default())
    }

    pub fn info(&mut self, msg: impl Into<String>) {
        self.message = Some((msg.into(), MessageKind::Info));
    }

    pub fn error(&mut self, msg: impl Into<String>) {
        self.message = Some((msg.into(), MessageKind::Error));
    }

    pub fn save(&mut self) {
        match AgentpackManifest::replace_modes(&self.project_root, &self.state.modes) {
            Ok(()) => {
                self.state.dirty = false;
                self.info("Saved.");
            }
            Err(error) => self.error(format!("Save failed: {error}")),
        }
    }
}

pub struct VisibleRow<'a> {
    pub node: &'a TreeNode,
    pub depth: usize,
    pub expandable: bool,
    pub expanded: bool,
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
