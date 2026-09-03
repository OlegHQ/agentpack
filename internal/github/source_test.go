package github

import "testing"

func TestParseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url  string
		want Source
	}{
		{"https://github.com/anthropics/skills/tree/main/skills/canvas-design", Source{"anthropics", "skills", "main", "skills/canvas-design"}},
		{"https://github.com/foo/bar/blob/main/skill/SKILL.md", Source{"foo", "bar", "main", "skill"}},
		{"https://github.com/foo/bar/blob/main/plugins/hookify/.claude-plugin/plugin.json", Source{"foo", "bar", "main", "plugins/hookify"}},
		{"https://github.com/foo/bar/blob/main/p/.cursor-plugin/plugin.json", Source{"foo", "bar", "main", "p"}},
		{"https://github.com/foo/bar/blob/main/p/.codex-plugin/plugin.json", Source{"foo", "bar", "main", "p"}},
		{"https://github.com/o/r/blob/main/plugins/pkg/agents/agent.md", Source{"o", "r", "main", "plugins/pkg/agents/agent.md"}},
		{"https://github.com/O/R.git", Source{"O", "R", DefaultGitRef, ""}},
	}
	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			got, err := ParseURL(test.url)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("ParseURL() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseURLRejectsUnsupportedHostsAndPaths(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"https://example.com/o/r", "https://github.com/o", "https://github.com/o/r/issues/1"} {
		if _, err := ParseURL(raw); err == nil {
			t.Fatalf("ParseURL(%q) succeeded", raw)
		}
	}
}

func TestNormalizedIdentityIgnoresRefAndOwnerRepoCase(t *testing.T) {
	t.Parallel()
	a := Source{Owner: "OlegHQ", Repo: "AgentPack", GitRef: "main", Path: "skills/x"}
	b := Source{Owner: "oleghq", Repo: "agentpack", GitRef: "HEAD", Path: "skills/x"}
	if NormalizedIdentity(a, "ABC") != NormalizedIdentity(b, "abc") {
		t.Fatal("normalized identities differ")
	}
}
