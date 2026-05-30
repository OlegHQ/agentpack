//! Pure reducer that owns the in-memory modes map. Kept independent of ratatui so its unit tests
//! exercise the core semantics (default-is-reserved, selector canonicalisation, dirty flag).

use std::collections::BTreeMap;

use crate::error::{AgentpackError, Result};
use crate::manifest::AgentpackManifest;

use crate::mode::selectors::Selector;
use crate::mode::{is_reserved_mode, validate_mode_name, ModeBase, ModeDefinition, DEFAULT_MODE_NAME};

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
