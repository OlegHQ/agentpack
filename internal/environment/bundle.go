package environment

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
	"github.com/pelletier/go-toml/v2"
)

const bundleMetadataName = "bundle.json"
const maxBundleFileSize = 4 << 20

type bundleMetadata struct {
	SchemaVersion int               `json:"schema_version"`
	Algorithm     string            `json:"algorithm"`
	Files         map[string]string `json:"files"`
}

// ExportBundle preserves definition bytes and refuses inputs that cannot safely travel.
func ExportBundle(definitionRoot, destination string) error {
	if err := validatePortableDefinition(definitionRoot); err != nil {
		return err
	}
	files := map[string][]byte{}
	metadata := bundleMetadata{SchemaVersion: 1, Algorithm: "sha256-file-v1", Files: map[string]string{}}
	for _, name := range []string{paths.ManifestName, paths.LockfileName, "contract.json"} {
		data, err := os.ReadFile(filepath.Join(definitionRoot, name))
		if os.IsNotExist(err) && name == "contract.json" {
			continue
		}
		if err != nil {
			return err
		}
		if len(data) > maxBundleFileSize {
			return fmt.Errorf("bundle file %s exceeds 4 MiB", name)
		}
		files[name] = data
		digest := sha256.Sum256(data)
		metadata.Files[name] = hex.EncodeToString(digest[:])
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	files[bundleMetadataName] = data
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".agentpack-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, name := range []string{paths.ManifestName, paths.LockfileName, "contract.json", bundleMetadataName} {
		data, ok := files[name]
		if !ok {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			file.Close()
			return err
		}
		if _, err := tw.Write(data); err != nil {
			file.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		file.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Linking a sibling temporary file publishes atomically without overwriting anything.
	if err := os.Link(file.Name(), destination); err != nil {
		return fmt.Errorf("publish bundle (destination must not exist): %w", err)
	}
	return nil
}

// ImportBundle validates privately before atomically publishing a new directory.
func ImportBundle(bundlePath, destination string) (string, error) {
	if destination == "" {
		return "", fmt.Errorf("import destination is required")
	}
	dir, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	publicationLock := flock.New(dir + ".import.lock")
	if err := publicationLock.Lock(); err != nil {
		return "", err
	}
	defer publicationLock.Unlock()
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		return "", fmt.Errorf("import destination must not exist: %s", dir)
	}
	temporary, err := os.MkdirTemp(filepath.Dir(dir), ".agentpack-import-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temporary)
	file, err := os.Open(bundlePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name := header.Name
		if name != paths.ManifestName && name != paths.LockfileName && name != "contract.json" && name != bundleMetadataName {
			return "", fmt.Errorf("unsupported or unsafe bundle entry %q", name)
		}
		if seen[name] {
			return "", fmt.Errorf("duplicate bundle entry %q", name)
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maxBundleFileSize {
			return "", fmt.Errorf("bundle entry %q must be a regular file of at most 4 MiB", name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxBundleFileSize+1))
		if err != nil {
			return "", err
		}
		if int64(len(data)) != header.Size {
			return "", fmt.Errorf("truncated bundle entry %q", name)
		}
		if err := os.WriteFile(filepath.Join(temporary, name), data, 0o600); err != nil {
			return "", err
		}
		seen[name] = true
	}
	// Read through the gzip trailer so corruption cannot be published.
	if n, err := io.Copy(io.Discard, io.LimitReader(gz, 1025)); err != nil {
		return "", err
	} else if n > 1024 {
		return "", fmt.Errorf("unexpected data after bundle entries")
	}
	if !seen[paths.ManifestName] || !seen[paths.LockfileName] || !seen[bundleMetadataName] {
		return "", fmt.Errorf("bundle missing agentpack.toml, pack.lock, or bundle.json; re-export with this version")
	}
	metadataBytes, err := os.ReadFile(filepath.Join(temporary, bundleMetadataName))
	if err != nil {
		return "", err
	}
	var metadata bundleMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return "", err
	}
	if metadata.SchemaVersion != 1 || metadata.Algorithm != "sha256-file-v1" {
		return "", fmt.Errorf("unsupported bundle schema or digest algorithm")
	}
	if len(metadata.Files) != len(seen)-1 {
		return "", fmt.Errorf("bundle digest manifest does not match entries")
	}
	for name := range seen {
		if name == bundleMetadataName {
			continue
		}
		data, err := os.ReadFile(filepath.Join(temporary, name))
		if err != nil {
			return "", err
		}
		digest := sha256.Sum256(data)
		if metadata.Files[name] != hex.EncodeToString(digest[:]) {
			return "", fmt.Errorf("bundle digest mismatch for %s", name)
		}
	}
	if err := validatePortableDefinition(temporary); err != nil {
		return "", err
	}
	if err := os.Remove(filepath.Join(temporary, bundleMetadataName)); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, dir); err != nil {
		return "", err
	}
	return dir, nil
}

var privatePathPattern = regexp.MustCompile(`(^|[\s="'])(/[^\s"']+|[A-Za-z]:[\\/][^\s"']+|~[/\\][^\s"']+)`)
var credentialPattern = regexp.MustCompile(`(?i)(api[_-]?key|(?:access[_-]?)?token|password|secret|authorization)\s*[=:]\s*[^\s"']+`)
var credentialFlagPattern = regexp.MustCompile(`(?i)^--?(?:api[_-]?key|token|access[_-]?token|password|secret|authorization)$`)
var credentialQueryPattern = regexp.MustCompile(`(?i)(token|secret|password|api[_-]?key|authorization)`)

func validatePortableDefinition(root string) error {
	mf, err := manifest.Load(root)
	if err != nil {
		return err
	}
	if mf == nil {
		return fmt.Errorf("export requires agentpack.toml")
	}
	for module, dependency := range mf.Dependencies {
		if _, local := dependency.PathValue(); local {
			return fmt.Errorf("cannot export host-local dependency %q; publish it as a pinned GitHub package first", module)
		}
	}
	for name, server := range mf.MCP.Servers {
		for key, value := range server.Env {
			if value != "" {
				return fmt.Errorf("cannot export inline MCP environment value for %s.%s; supply it through the launching environment instead", name, key)
			}
		}
	}
	locked, err := lockfile.Load(root)
	if err != nil {
		return err
	}
	for _, pkg := range locked.Packages {
		if pkg.Owner == "path" || pkg.Owner == "local" || strings.HasPrefix(pkg.URL, "agentpack-local:") || strings.HasPrefix(pkg.URL, "file:") {
			return fmt.Errorf("cannot export host-local locked package %q; publish it as a pinned GitHub package first", pkg.Module)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "contract.json")); err == nil {
		if _, err := LoadContract(filepath.Join(root, "contract.json")); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, name := range []string{paths.ManifestName, paths.LockfileName, "contract.json"} {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && name == "contract.json" {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("bundle input %s must be a regular file, not a symlink", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var document any
		if name == "contract.json" {
			err = json.Unmarshal(data, &document)
		} else {
			err = toml.Unmarshal(data, &document)
		}
		if err != nil {
			return err
		}
		if err := checkPortableValues(document); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func checkPortableValues(value any) error {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if err := checkPortableValues(key); err != nil {
				return err
			}
			if err := checkPortableValues(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if text, ok := child.(string); ok && credentialFlagPattern.MatchString(text) {
				return fmt.Errorf("inline credential argument cannot be exported")
			}
			if err := checkPortableValues(child); err != nil {
				return err
			}
		}
	case string:
		if privatePathPattern.MatchString(value) || strings.HasPrefix(value, "file:") {
			return fmt.Errorf("host-private path cannot be exported")
		}
		if parsed, err := url.Parse(value); err == nil && parsed.IsAbs() {
			if parsed.User != nil {
				return fmt.Errorf("URL credentials cannot be exported")
			}
			for key := range parsed.Query() {
				if credentialQueryPattern.MatchString(key) {
					return fmt.Errorf("URL credential query cannot be exported")
				}
			}
		}
		if credentialPattern.MatchString(value) {
			return fmt.Errorf("inline credential assignment cannot be exported")
		}
	}
	return nil
}
