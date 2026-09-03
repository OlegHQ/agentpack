package proxy

import (
	"os"
	"strings"
)

var AllowedModels = []string{"gpt-5.2", "gpt-5.3-codex", "gpt-5.3-codex-spark", "gpt-5.4", "gpt-5.4-mini", "gpt-5.5"}

type ModelMap struct{ Big, Middle, Small string }
type ProxyModel struct{ Requested, Upstream, ServiceTier string }

func DefaultModelMap() ModelMap {
	return ModelMap{Big: "gpt-5.5", Middle: "gpt-5.4", Small: "gpt-5.4-mini"}
}
func ModelMapFromEnvironment() ModelMap {
	defaults := DefaultModelMap()
	return ModelMap{Big: envOr("AGENTPACK_PROXY_BIG_MODEL", defaults.Big), Middle: envOr("AGENTPACK_PROXY_MIDDLE_MODEL", defaults.Middle), Small: envOr("AGENTPACK_PROXY_SMALL_MODEL", defaults.Small)}
}
func (models ModelMap) Resolve(requested string) ProxyModel {
	lower := strings.ToLower(requested)
	resolved := resolveModel(requested)
	upstream := resolved.Upstream
	if !explicitOpenAI(lower) {
		if strings.Contains(lower, "haiku") {
			upstream = models.Small
		} else if strings.Contains(lower, "sonnet") {
			upstream = models.Middle
		} else {
			upstream = models.Big
		}
	}
	return ProxyModel{Requested: requested, Upstream: upstream, ServiceTier: resolved.ServiceTier}
}
func (models ModelMap) List() map[string]any {
	aliases := [][3]string{{"claude-opus-4-7", "Claude Opus via Codex", models.Big}, {"claude-opus-4-8", "Claude Opus via Codex", models.Big}, {"claude-sonnet-4-6", "Claude Sonnet via Codex", models.Middle}, {"claude-haiku-4-5", "Claude Haiku via Codex", models.Small}}
	data := []any{}
	for _, entry := range aliases {
		data = append(data, modelEntry(entry[0], entry[1], entry[2]))
	}
	for _, model := range AllowedModels {
		data = append(data, modelEntry(model, "Codex", model), modelEntry(model+"-fast", "Codex", model+"-fast"))
	}
	return map[string]any{"data": data, "has_more": false}
}
func resolveModel(model string) ProxyModel {
	aliases := map[string]string{"haiku": "gpt-5.4-mini", "claude-haiku-4-5": "gpt-5.4-mini", "claude-haiku-4-5-20251001": "gpt-5.4-mini", "sonnet": "gpt-5.4", "claude-sonnet-4-6": "gpt-5.4", "opus": "gpt-5.5", "claude-opus-4-7": "gpt-5.5"}
	if value := aliases[model]; value != "" {
		model = value
	}
	tier := ""
	if strings.HasSuffix(model, "-fast") && allowed(strings.TrimSuffix(model, "-fast")) {
		model = strings.TrimSuffix(model, "-fast")
		tier = "priority"
	}
	return ProxyModel{Upstream: model, ServiceTier: tier}
}
func allowed(model string) bool {
	for _, value := range AllowedModels {
		if value == model {
			return true
		}
	}
	return false
}
func explicitOpenAI(model string) bool {
	for _, prefix := range []string{"gpt-", "o1-", "o3-", "o4-", "o5-"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}
func modelEntry(id, display, mapped string) map[string]any {
	return map[string]any{"id": id, "type": "model", "display_name": display + " (" + mapped + ")", "created_at": "2026-01-01T00:00:00Z"}
}
func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
