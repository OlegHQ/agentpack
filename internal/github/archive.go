package github

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func CollectRepoRelativePaths(archive io.Reader) (map[string]struct{}, error) {
	reader, err := tarReader(archive)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{})
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return paths, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if relative, ok := repoRelative(header.Name); ok {
			paths[relative] = struct{}{}
		}
	}
}

func ExtractTarballWithPrefix(archive io.Reader, prefix, destination string) (int, error) {
	reader, err := tarReader(archive)
	if err != nil {
		return 0, err
	}
	if err := os.RemoveAll(destination); err != nil {
		return 0, fmt.Errorf("reset archive destination %s: %w", destination, err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return 0, fmt.Errorf("create archive destination %s: %w", destination, err)
	}
	prefix = strings.Trim(prefix, "/")
	files := 0
	links := make(map[string]bool)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return 0, fmt.Errorf("read archive: %w", err)
		}
		relative, ok := repoRelative(header.Name)
		if !ok {
			continue
		}
		extractRelative, ok := stripRepoPrefix(relative, prefix)
		if !ok {
			continue
		}
		output, err := safeExtractPath(destination, extractRelative)
		if err != nil {
			return 0, err
		}
		if belowLink(links, extractRelative) {
			// Git never stores an entry below a link; writing one would
			// leave the destination through it.
			continue
		}
		if header.Typeflag == tar.TypeSymlink && LinkStaysInside(extractRelative, header.Linkname) {
			if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
				return 0, fmt.Errorf("create archive parent %s: %w", filepath.Dir(output), err)
			}
			if os.Symlink(header.Linkname, output) == nil {
				links[extractRelative] = true
				files++
				continue
			}
			// Where links cannot be created the entry stays an empty file.
		}
		if header.FileInfo().IsDir() {
			if err := os.MkdirAll(output, 0o755); err != nil {
				return 0, fmt.Errorf("create archive directory %s: %w", output, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return 0, fmt.Errorf("create archive parent %s: %w", filepath.Dir(output), err)
		}
		file, err := os.OpenFile(output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, fmt.Errorf("create archive file %s: %w", output, err)
		}
		_, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return 0, fmt.Errorf("write archive file %s: %v", output, firstError(copyErr, closeErr))
		}
		files++
	}
}

// LinkStaysInside reports whether a symbolic link at relative (inside the
// extracted tree) may be created as written. The target must be a clean
// relative path — any ".." only at its start — that still lies inside the
// tree, so that following it, however links are chained, cannot leave the
// tree. Every other link (absolute, or reaching a part of the repository
// that is not extracted) is written as an empty file, as before.
func LinkStaysInside(relative, target string) bool {
	if target == "" || strings.ContainsRune(target, '\\') || strings.ContainsRune(target, 0) || path.IsAbs(target) || filepath.VolumeName(target) != "" {
		return false
	}
	clean := path.Clean(target)
	if trimmed := strings.TrimRight(target, "/"); trimmed != clean && trimmed != "./"+clean {
		return false
	}
	resolved := path.Join(path.Dir(relative), clean)
	return resolved != ".." && !strings.HasPrefix(resolved, "../")
}

func belowLink(links map[string]bool, relative string) bool {
	for current := path.Dir(relative); current != "." && current != "/"; current = path.Dir(current) {
		if links[current] {
			return true
		}
	}
	return false
}

func PathLooksLikeFile(inRepoPath string) bool {
	base := path.Base(strings.Trim(inRepoPath, "/"))
	return base != "." && base != "SKILL.md" && path.Ext(base) != ""
}

func ParentDirInRepo(inRepoPath string) string {
	return cleanRepoParent(path.Dir(strings.Trim(inRepoPath, "/")))
}

func ChoosePackagePrefix(paths map[string]struct{}, blobPath string, packageRoot func(map[string]struct{}, string) bool) (string, bool) {
	current := path.Dir(strings.Trim(blobPath, "/"))
	for current != "." && current != "/" {
		if packageRoot(paths, current) {
			return current, true
		}
		current = path.Dir(current)
	}
	if packageRoot(paths, "") {
		return "", true
	}
	return "", false
}

func tarReader(input io.Reader) (*tar.Reader, error) {
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return nil, fmt.Errorf("decode gzip archive: %w", err)
	}
	return tar.NewReader(gzipReader), nil
}

func repoRelative(name string) (string, bool) {
	name = strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./")
	_, relative, found := strings.Cut(name, "/")
	return relative, found && relative != ""
}

func stripRepoPrefix(relative, prefix string) (string, bool) {
	relative = strings.TrimSuffix(relative, "/")
	if prefix == "" {
		return relative, relative != ""
	}
	if relative == prefix {
		return "", false
	}
	return strings.CutPrefix(relative, prefix+"/")
}

func safeExtractPath(destination, relative string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(relative, "\\", "/"))
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || clean == "." {
		return "", fmt.Errorf("unsafe path in archive entry (path traversal): %s", relative)
	}
	return filepath.Join(destination, filepath.FromSlash(clean)), nil
}

func firstError(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}

// ReadRepoFiles returns the contents of the named repository-relative files
// that the archive holds.
func ReadRepoFiles(archive io.Reader, names ...string) (map[string][]byte, error) {
	reader, err := tarReader(archive)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	found := make(map[string][]byte)
	for len(found) < len(wanted) {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		relative, ok := repoRelative(header.Name)
		if !ok || !wanted[relative] || header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(reader, 16<<20))
		if err != nil {
			return nil, fmt.Errorf("read archive file %s: %w", relative, err)
		}
		found[relative] = data
	}
	return found, nil
}
