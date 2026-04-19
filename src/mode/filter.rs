use std::path::Path;

use crate::error::{AgentpackError, Result};
use crate::manifest::AgentpackManifest;

use super::catalog::CapabilityCatalog;
use super::selectors::{MatchSpecificity, Selector};
use super::{ModeBase, ModeDefinition, DEFAULT_MODE_NAME};

#[derive(Clone, Debug)]
pub struct EffectiveMode {
    name: String,
    definition: ModeDefinition,
    enabled_selectors: Vec<Selector>,
    disabled_selectors: Vec<Selector>,
}

impl EffectiveMode {
    /// Build an [`EffectiveMode`] from an in-memory definition without catalog validation.
    /// Used by the TUI, which wants live previews while the user edits selectors that may
    /// not yet be fully valid; production staging always goes through [`Self::resolve`].
    pub fn from_definition(name: &str, definition: ModeDefinition) -> Result<Self> {
        let enabled_selectors = definition
            .enable
            .iter()
            .map(|raw| Selector::parse(raw))
            .collect::<Result<Vec<_>>>()?;
        let disabled_selectors = definition
            .disable
            .iter()
            .map(|raw| Selector::parse(raw))
            .collect::<Result<Vec<_>>>()?;
        Ok(Self {
            name: name.to_string(),
            definition,
            enabled_selectors,
            disabled_selectors,
        })
    }

    pub fn resolve(
        manifest: Option<&AgentpackManifest>,
        selected_mode: Option<&str>,
        catalog: Option<&CapabilityCatalog>,
    ) -> Result<Self> {
        let name = selected_mode.unwrap_or(DEFAULT_MODE_NAME);
        let definition = match manifest {
            Some(manifest) => manifest
                .mode_definition(name)
                .ok_or_else(|| AgentpackError::Mode(format!("unknown mode: {name}")))?,
            None if name == DEFAULT_MODE_NAME => ModeDefinition::implicit_default(),
            None => return Err(AgentpackError::Mode(format!("unknown mode: {name}"))),
        };

        let mut enabled_selectors = Vec::with_capacity(definition.enable.len());
        for raw in &definition.enable {
            let selector = Selector::parse(raw)?;
            if let Some(catalog) = catalog {
                catalog.validate_selector(&selector)?;
            }
            enabled_selectors.push(selector);
        }

        let mut disabled_selectors = Vec::with_capacity(definition.disable.len());
        for raw in &definition.disable {
            let selector = Selector::parse(raw)?;
            if let Some(catalog) = catalog {
                catalog.validate_selector(&selector)?;
            }
            disabled_selectors.push(selector);
        }

        Ok(Self {
            name: name.to_string(),
            definition,
            enabled_selectors,
            disabled_selectors,
        })
    }

    pub fn implicit_default() -> Self {
        Self {
            name: DEFAULT_MODE_NAME.into(),
            definition: ModeDefinition::implicit_default(),
            enabled_selectors: Vec::new(),
            disabled_selectors: Vec::new(),
        }
    }

    pub fn name(&self) -> &str {
        &self.name
    }

    pub fn definition(&self) -> &ModeDefinition {
        &self.definition
    }

    pub fn base(&self) -> ModeBase {
        self.definition.base
    }

    pub fn allows_package_path(&self, module: &str, rel_path: &Path) -> Result<bool> {
        Ok(self
            .resolve_path_decision(
                self.enabled_selectors
                    .iter()
                    .map(|selector| selector.matches_package_path(module, rel_path))
                    .collect::<Result<Vec<_>>>()?,
                self.disabled_selectors
                    .iter()
                    .map(|selector| selector.matches_package_path(module, rel_path))
                    .collect::<Result<Vec<_>>>()?,
            )
            .unwrap_or(matches!(self.definition.base, ModeBase::All)))
    }

    pub fn allows_dot_agents_path(&self, rel_path: &Path) -> Result<bool> {
        Ok(self
            .resolve_path_decision(
                self.enabled_selectors
                    .iter()
                    .map(|selector| selector.matches_dot_agents_path(rel_path))
                    .collect::<Result<Vec<_>>>()?,
                self.disabled_selectors
                    .iter()
                    .map(|selector| selector.matches_dot_agents_path(rel_path))
                    .collect::<Result<Vec<_>>>()?,
            )
            .unwrap_or(matches!(self.definition.base, ModeBase::All)))
    }

    pub fn allows_mcp(&self, name: &str) -> bool {
        self.resolve_exact_decision(
            self.enabled_selectors
                .iter()
                .filter(|selector| selector.matches_mcp(name))
                .count(),
            self.disabled_selectors
                .iter()
                .filter(|selector| selector.matches_mcp(name))
                .count(),
        )
        .unwrap_or(matches!(self.definition.base, ModeBase::All))
    }

    pub fn fingerprint_material(&self) -> String {
        let mut normalized = self.definition.clone();
        normalized.sort_and_dedup();
        format!(
            "mode={}\nbase={}\nenable={:?}\ndisable={:?}\n",
            self.name, normalized.base, normalized.enable, normalized.disable
        )
    }

    fn resolve_path_decision(
        &self,
        enabled: Vec<Option<MatchSpecificity>>,
        disabled: Vec<Option<MatchSpecificity>>,
    ) -> Option<bool> {
        let best_enabled = enabled.into_iter().flatten().max();
        let best_disabled = disabled.into_iter().flatten().max();
        match (best_enabled, best_disabled) {
            (Some(left), Some(right)) if left > right => Some(true),
            (Some(left), Some(right)) if right > left => Some(false),
            (Some(_), Some(_)) => Some(false),
            (Some(_), None) => Some(true),
            (None, Some(_)) => Some(false),
            (None, None) => None,
        }
    }

    fn resolve_exact_decision(
        &self,
        enabled_matches: usize,
        disabled_matches: usize,
    ) -> Option<bool> {
        match (enabled_matches > 0, disabled_matches > 0) {
            (true, true) => Some(false),
            (true, false) => Some(true),
            (false, true) => Some(false),
            (false, false) => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mode::ModeBase;

    #[test]
    fn package_path_enable_overrides_disabled_package() {
        let mode = EffectiveMode {
            name: "test".into(),
            definition: ModeDefinition {
                base: ModeBase::All,
                enable: vec!["package-path:github.com/acme/repo:hooks/hooks.json".into()],
                disable: vec!["package:github.com/acme/repo".into()],
            },
            enabled_selectors: vec![Selector::parse(
                "package-path:github.com/acme/repo:hooks/hooks.json",
            )
            .unwrap()],
            disabled_selectors: vec![Selector::parse("package:github.com/acme/repo").unwrap()],
        };

        assert!(mode
            .allows_package_path("github.com/acme/repo", Path::new("hooks/hooks.json"))
            .unwrap());
        assert!(!mode
            .allows_package_path("github.com/acme/repo", Path::new("agents/foo.md"))
            .unwrap());
    }

    #[test]
    fn base_none_requires_explicit_enable() {
        let mode = EffectiveMode {
            name: "test".into(),
            definition: ModeDefinition {
                base: ModeBase::None,
                enable: vec!["mcp:filesystem".into()],
                disable: Vec::new(),
            },
            enabled_selectors: vec![Selector::parse("mcp:filesystem").unwrap()],
            disabled_selectors: Vec::new(),
        };
        assert!(mode.allows_mcp("filesystem"));
        assert!(!mode.allows_mcp("github"));
    }
}
