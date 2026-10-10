package environment

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/OlegHQ/agentpack/internal/paths"
)

// ExportBundle writes definitionRoot's agentpack.toml and pack.lock into a
// gzipped tar at destination. Absolute host paths and credentials are not
// included — only the portable definition files.
func ExportBundle(definitionRoot, destination string) error {
	manifestPath := paths.ManifestPath(definitionRoot)
	lockPath := paths.LockPath(definitionRoot)
	for _, path := range []string{manifestPath, lockPath} {
		if !regularFile(path) {
			return fmt.Errorf("export requires %s", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer file.Close()
	gz := gzip.NewWriter(file)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for _, name := range []string{paths.ManifestName, paths.LockfileName} {
		if err := writeTarFile(tw, filepath.Join(definitionRoot, name), name); err != nil {
			return err
		}
	}
	return nil
}

// ImportBundle extracts a bundle into destination (created if needed).
func ImportBundle(bundlePath, destination string) (string, error) {
	if destination == "" {
		return "", fmt.Errorf("import destination is required")
	}
	dir, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
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
		name := filepath.Clean(header.Name)
		if name == "." || strings.Contains(name, "..") || filepath.IsAbs(name) {
			return "", fmt.Errorf("refusing unsafe bundle entry %q", header.Name)
		}
		base := filepath.Base(name)
		if base != paths.ManifestName && base != paths.LockfileName {
			continue
		}
		target := filepath.Join(dir, base)
		if header.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("bundle entry %s is not a regular file", header.Name)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return "", err
		}
		out.Close()
		seen[base] = true
	}
	if !seen[paths.ManifestName] || !seen[paths.LockfileName] {
		return "", fmt.Errorf("bundle missing %s or %s", paths.ManifestName, paths.LockfileName)
	}
	return dir, nil
}

func writeTarFile(tw *tar.Writer, source, name string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = name
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(tw, file)
	return err
}
