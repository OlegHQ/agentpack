package environment

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func portableFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, data := range map[string]string{"agentpack.toml": "name = \"team\"\n[dependencies]\n", "pack.lock": "lockfile_version = 2\n[meta]\nname = \"team\"\nversion = \"1\"\n", "contract.json": "{\"schema_version\":1,\"requirements\":[]}"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPortableBundlePreservesContractAndRefusesOverwrite(t *testing.T) {
	root := portableFixture(t)
	bundle := filepath.Join(t.TempDir(), "team.bundle")
	if err := ExportBundle(root, bundle); err != nil {
		t.Fatal(err)
	}
	if err := ExportBundle(root, bundle); err == nil {
		t.Fatal("export overwrote existing file")
	}
	destination := filepath.Join(t.TempDir(), "imported")
	if _, err := ImportBundle(bundle, destination); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agentpack.toml", "pack.lock", "contract.json"} {
		before, _ := os.ReadFile(filepath.Join(root, name))
		after, _ := os.ReadFile(filepath.Join(destination, name))
		if string(before) != string(after) {
			t.Fatalf("%s changed", name)
		}
	}
	if _, err := ImportBundle(bundle, destination); err == nil {
		t.Fatal("import overwrote existing environment")
	}
}

func malformedBundle(t *testing.T, entries []tar.Header, bodies []string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "bad.bundle")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for i, header := range entries {
		header.Size = int64(len(bodies[i]))
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(bodies[i])); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	file.Close()
	return name
}

func TestInvalidBundleNeverPublishesOrClobbers(t *testing.T) {
	for _, test := range []struct {
		name    string
		headers []tar.Header
		bodies  []string
	}{
		{"partial", []tar.Header{{Name: "agentpack.toml", Typeflag: tar.TypeReg}}, []string{"name = \"replacement\""}},
		{"traversal", []tar.Header{{Name: "../agentpack.toml", Typeflag: tar.TypeReg}}, []string{"bad"}},
		{"nested", []tar.Header{{Name: "nested/agentpack.toml", Typeflag: tar.TypeReg}}, []string{"bad"}},
		{"duplicate", []tar.Header{{Name: "agentpack.toml", Typeflag: tar.TypeReg}, {Name: "agentpack.toml", Typeflag: tar.TypeReg}}, []string{"bad", "bad"}},
		{"symlink", []tar.Header{{Name: "agentpack.toml", Typeflag: tar.TypeSymlink, Linkname: "/private/secret"}}, []string{""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := malformedBundle(t, test.headers, test.bodies)
			destination := filepath.Join(t.TempDir(), "new")
			if _, err := ImportBundle(bundle, destination); err == nil {
				t.Fatal("accepted malformed bundle")
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatal("failed import published directory")
			}
			existing := portableFixture(t)
			before, _ := os.ReadFile(filepath.Join(existing, "agentpack.toml"))
			if _, err := ImportBundle(bundle, existing); err == nil {
				t.Fatal("accepted existing destination")
			}
			after, _ := os.ReadFile(filepath.Join(existing, "agentpack.toml"))
			if string(before) != string(after) {
				t.Fatal("failed import clobbered manifest")
			}
		})
	}
}

func TestPortableExportRejectsPrivateInputsWithoutCreatingOutput(t *testing.T) {
	for _, body := range []string{
		"name=\"team\"\n[dependencies]\nlocal={path=\"../local\"}\n",
		"name=\"team\"\n[mcp.servers.demo]\ncommand=\"example\"\nenv={API_KEY=\"sentinel-secret\"}\n",
		"name=\"team\"\n[mcp.servers.demo]\ncommand=\"/Users/private/bin/tool\"\n",
		"name=\"team\"\n[mcp.servers.demo]\ncommand=\"tool\"\nargs=[\"--token=sentinel-secret\"]\n",
	} {
		root := portableFixture(t)
		os.WriteFile(filepath.Join(root, "agentpack.toml"), []byte(body), 0o600)
		destination := filepath.Join(t.TempDir(), "export")
		if err := ExportBundle(root, destination); err == nil {
			t.Fatalf("export accepted private manifest %s", body)
		} else if strings.Contains(err.Error(), "sentinel-secret") {
			t.Fatal("error leaked secret")
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatal("failed export created output")
		}
	}
}

func TestSupportCapsuleOmitsArbitraryNativeSecrets(t *testing.T) {
	receipt := Receipt{ReceiptID: "private-receipt", Mode: "private-team", Target: ReceiptTarget{Adapter: "codex", NativeVersion: "codex-cli 0.159.3"}, Findings: []Finding{{Code: "NATIVE_PROBE_FAILED", Message: "failed: bearer sentinel-secret at /Users/private/work", Remedy: "token=sentinel-secret", Source: "/Users/private/work", Winner: "private-skill"}}, Properties: []ObservedProperty{{ArtifactID: "private-skill", Property: "secret_property", Value: map[string]any{"token": "sentinel-secret"}, Reason: "sentinel-secret", EvidenceRef: "/Users/private/evidence"}}}
	data, err := json.Marshal(ExportSupportCapsule(receipt))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"sentinel-secret", "/Users/private", "private-skill", "private-team", "private-receipt"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("capsule leaked %s: %s", secret, data)
		}
	}
	if !strings.Contains(string(data), "0.159.3") {
		t.Fatal("public version omitted")
	}
}

func TestBundleRejectsDigestMismatchAndFutureSchema(t *testing.T) {
	root := portableFixture(t)
	bundle := filepath.Join(t.TempDir(), "original.bundle")
	if err := ExportBundle(root, bundle); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var headers []tar.Header
	var bodies []string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		headers = append(headers, *header)
		bodies = append(bodies, string(body))
	}
	for _, test := range []string{"digest", "schema"} {
		t.Run(test, func(t *testing.T) {
			changed := append([]string(nil), bodies...)
			for i, header := range headers {
				if test == "digest" && header.Name == "agentpack.toml" {
					changed[i] = "name=\"changed\"\n"
				}
				if test == "schema" && header.Name == bundleMetadataName {
					changed[i] = strings.Replace(changed[i], `"schema_version":1`, `"schema_version":2`, 1)
				}
			}
			bad := malformedBundle(t, headers, changed)
			destination := filepath.Join(t.TempDir(), "new")
			if _, err := ImportBundle(bad, destination); err == nil {
				t.Fatal("invalid bundle accepted")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("invalid bundle published")
			}
		})
	}
}

func TestBundleRejectsOversizedEntry(t *testing.T) {
	name := filepath.Join(t.TempDir(), "huge.bundle")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "agentpack.toml", Mode: 0o600, Typeflag: tar.TypeReg, Size: maxBundleFileSize + 1}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	file.Close()
	if _, err := ImportBundle(name, filepath.Join(t.TempDir(), "new")); err == nil {
		t.Fatal("oversized entry accepted")
	}
}

func TestPortableExportRejectsHostLocalAuthoritativeLock(t *testing.T) {
	root := portableFixture(t)
	body := `lockfile_version = 2
[meta]
name = "team"
version = "1"
[[packages]]
module = "helper"
kind = "skill"
owner = "local"
repo = "helper"
url = "agentpack-local:helper"
commit = "local"
cache_key = "helper"
`
	if err := os.WriteFile(filepath.Join(root, "pack.lock"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ExportBundle(root, filepath.Join(t.TempDir(), "bundle")); err == nil {
		t.Fatal("host-local authoritative lock accepted")
	}
}
