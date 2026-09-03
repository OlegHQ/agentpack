package cache

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"

	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func BlobParentPrefixes(blobPath string) []string {
	current := path.Dir(strings.Trim(blobPath, "/"))
	var prefixes []string
	for current != "." && current != "/" {
		prefixes = append(prefixes, current)
		current = path.Dir(current)
	}
	return append(prefixes, "")
}

func MaterializeGitHubTree(ctx context.Context, client *http.Client, source githubsource.Source, displayURL string, forceRefresh bool) (lockfile.Package, error) {
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		return lockfile.Package{}, err
	}
	commit, err := githubsource.ResolveRef(ctx, client, source.Owner, source.Repo, source.GitRef, forceRefresh)
	if err != nil {
		return lockfile.Package{}, err
	}
	effective := source
	var prefetched []byte
	if strings.Contains(displayURL, "/blob/") && githubsource.PathLooksLikeFile(source.Path) {
		for _, prefix := range BlobParentPrefixes(source.Path) {
			candidate := source
			candidate.Path = prefix
			key := ComputeKey(githubsource.NormalizedIdentity(candidate, commit))
			out, entryErr := EntryDir(key)
			if entryErr == nil && IsPackageRoot(out) {
				effective.Path = prefix
				break
			}
		}
		if effective.Path == source.Path {
			prefetched, err = githubsource.DownloadTarball(ctx, client, source.Owner, source.Repo, commit)
			if err != nil {
				return lockfile.Package{}, err
			}
			index, err := githubsource.CollectRepoRelativePaths(bytes.NewReader(prefetched))
			if err != nil {
				return lockfile.Package{}, err
			}
			if prefix, ok := githubsource.ChoosePackagePrefix(index, source.Path, RepoDirIsPackageRoot); ok {
				effective.Path = prefix
			} else {
				effective.Path = githubsource.ParentDirInRepo(source.Path)
			}
		}
	} else {
		effective.Path = strings.Trim(source.Path, "/")
	}
	cacheKey := ComputeKey(githubsource.NormalizedIdentity(effective, commit))
	out, err := EntryDir(cacheKey)
	if err != nil {
		return lockfile.Package{}, err
	}
	if !IsPackageRoot(out) {
		if len(prefetched) == 0 {
			prefetched, err = githubsource.DownloadTarball(ctx, client, source.Owner, source.Repo, commit)
			if err != nil {
				return lockfile.Package{}, err
			}
		}
		written, err := githubsource.ExtractTarballWithPrefix(bytes.NewReader(prefetched), effective.Path, out)
		if err != nil {
			return lockfile.Package{}, err
		}
		if written == 0 && effective.Path != "" {
			return lockfile.Package{}, fmt.Errorf("no files matched repository path %q in %s/%s archive at %.8s", effective.Path, effective.Owner, effective.Repo, commit)
		}
	}
	return ClassifyMaterialized(out, githubsource.CanonicalTreeURL(effective), effective, commit, cacheKey)
}

func FetchGitHubAssetURL(ctx context.Context, client *http.Client, rawURL string) (lockfile.Package, error) {
	source, err := githubsource.ParseURL(rawURL)
	if err != nil {
		return lockfile.Package{}, err
	}
	return MaterializeGitHubTree(ctx, client, source, rawURL, false)
}

func GitHubRestore(ctx context.Context, client *http.Client) RemoteRestoreFunc {
	return func(pkg lockfile.Package, destination string) error {
		data, err := githubsource.DownloadTarball(ctx, client, pkg.Owner, pkg.Repo, pkg.Commit)
		if err != nil {
			return err
		}
		written, err := githubsource.ExtractTarballWithPrefix(bytes.NewReader(data), pkg.Path, destination)
		if err != nil {
			return err
		}
		if written == 0 && pkg.Path != "" {
			_ = os.RemoveAll(destination)
			return fmt.Errorf("no files matched repository path %q", pkg.Path)
		}
		return nil
	}
}
