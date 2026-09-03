package github

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestResolveRefUsesFullSHAAndFreshCache(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	sha := "A123456789abcdef0123456789abcdef01234567"
	got, err := ResolveRef(context.Background(), nil, "owner", "repo", sha, false)
	if err != nil || got != "a123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("full SHA = %q, %v", got, err)
	}
	if err := storeCachedRef("Owner", "Repo", "main", "b123456789abcdef0123456789abcdef01234567"); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveRef(context.Background(), &http.Client{}, "owner", "repo", "main", false)
	if err != nil || got != "b123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("cached SHA = %q, %v", got, err)
	}
}

func TestResolveRefAndListTagsUseRESTAndPersist(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	sha := "c123456789abcdef0123456789abcdef01234567"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/repos/o/r/commits/main" {
			fmt.Fprintf(response, `{"sha":%q}`, sha)
			return
		}
		fmt.Fprintf(response, `[{"name":"v2.0.0","commit":{"sha":%q}},{"name":"v1.0.0","commit":{"sha":%q}}]`, sha, sha)
	}))
	defer server.Close()
	previous := apiRoot
	apiRoot = server.URL
	defer func() { apiRoot = previous }()
	got, err := ResolveRef(context.Background(), server.Client(), "o", "r", "main", false)
	if err != nil || got != sha {
		t.Fatalf("ResolveRef() = %q, %v", got, err)
	}
	tags, err := ListTags(context.Background(), server.Client(), "o", "r", false)
	if err != nil || len(tags) != 2 || tags[0].Name != "v1.0.0" {
		t.Fatalf("ListTags() = %#v, %v", tags, err)
	}
}

func TestLSRemoteParsingPrefersPeeledTags(t *testing.T) {
	t.Parallel()
	base := "a123456789abcdef0123456789abcdef01234567"
	peeled := "b123456789abcdef0123456789abcdef01234567"
	output := base + "\trefs/tags/v1.0.0\n" + peeled + "\trefs/tags/v1.0.0^{}\n"
	sha, err := resolveSHAFromLSRemote(output, "v1.0.0")
	if err != nil || sha != peeled {
		t.Fatalf("resolveSHAFromLSRemote() = %q, %v", sha, err)
	}
	tags := tagPairsFromLSRemote(output)
	if len(tags) != 1 || tags[0] != (Tag{Name: "v1.0.0", SHA: peeled}) {
		t.Fatalf("tagPairsFromLSRemote() = %#v", tags)
	}
}

func TestFreshTagCacheResolvesExactTagWithoutNetwork(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	sha := "d123456789abcdef0123456789abcdef01234567"
	if err := storeCachedTags("owner", "repo", []Tag{{Name: "v1.2.3", SHA: sha}}); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveRef(context.Background(), &http.Client{}, "owner", "repo", "v1.2.3", false)
	if err != nil || got != sha {
		t.Fatalf("ResolveRef() = %q, %v", got, err)
	}
}

func TestEmbeddedGitProtocolFallback(t *testing.T) {
	if os.Getenv("AGENTPACK_NETWORK_TESTS") != "1" {
		t.Skip("set AGENTPACK_NETWORK_TESTS=1 to exercise GitHub's smart HTTP endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := gitLSRemote(ctx, "OlegHQ", "agentpack", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	sha, err := resolveSHAFromLSRemote(output, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	archive, err := fetchTarballWithGit(ctx, "OlegHQ", "agentpack", sha)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := CollectRepoRelativePaths(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := paths["README.md"]; !ok {
		t.Fatalf("archive does not contain README.md: %v", paths)
	}
}
