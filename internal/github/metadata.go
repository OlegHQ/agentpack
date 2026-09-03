package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/paths"
	bolt "go.etcd.io/bbolt"
)

const (
	refCacheTTL = 15 * time.Minute
	tagCacheTTL = time.Hour
)

var (
	refCacheBucket = []byte("github_ref_cache")
	tagCacheBucket = []byte("github_tag_cache")
)

type cachedRef struct {
	SHA           string `json:"sha"`
	CheckedAtUnix int64  `json:"checked_at_unix"`
}

type cachedTags struct {
	Tags          []Tag `json:"tags"`
	CheckedAtUnix int64 `json:"checked_at_unix"`
}

type Tag struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

func loadCachedRef(owner, repo, gitRef string) (cachedRef, bool, error) {
	var result cachedRef
	found, err := readMetadata(refCacheBucket, refCacheKey(owner, repo, gitRef), &result)
	return result, found, err
}

func storeCachedRef(owner, repo, gitRef, sha string) error {
	return writeMetadata(refCacheBucket, refCacheKey(owner, repo, gitRef), cachedRef{SHA: strings.ToLower(strings.TrimSpace(sha)), CheckedAtUnix: time.Now().Unix()})
}

func loadCachedTags(owner, repo string) (cachedTags, bool, error) {
	var result cachedTags
	found, err := readMetadata(tagCacheBucket, repoCacheKey(owner, repo), &result)
	return result, found, err
}

func storeCachedTags(owner, repo string, tags []Tag) error {
	return writeMetadata(tagCacheBucket, repoCacheKey(owner, repo), cachedTags{Tags: append([]Tag(nil), tags...), CheckedAtUnix: time.Now().Unix()})
}

func isFresh(checkedAt int64, ttl time.Duration) bool {
	return time.Since(time.Unix(checkedAt, 0)) <= ttl
}

func readMetadata(bucket, key []byte, destination any) (bool, error) {
	databasePath, err := paths.CacheDBPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect cache database %s: %w", databasePath, err)
	}
	database, err := bolt.Open(databasePath, 0o600, &bolt.Options{ReadOnly: true, Timeout: 2 * time.Second})
	if err != nil {
		return false, fmt.Errorf("open cache database %s: %w", databasePath, err)
	}
	found := false
	viewErr := database.View(func(transaction *bolt.Tx) error {
		table := transaction.Bucket(bucket)
		if table == nil {
			return nil
		}
		value := table.Get(key)
		if value == nil {
			return nil
		}
		found = true
		return json.Unmarshal(value, destination)
	})
	closeErr := database.Close()
	if viewErr != nil || closeErr != nil {
		return false, errors.Join(viewErr, closeErr)
	}
	return found, nil
}

func writeMetadata(bucket, key []byte, value any) error {
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		return err
	}
	databasePath, err := paths.CacheDBPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("serialize GitHub metadata: %w", err)
	}
	database, err := bolt.Open(databasePath, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return fmt.Errorf("open cache database %s: %w", databasePath, err)
	}
	updateErr := database.Update(func(transaction *bolt.Tx) error {
		table, err := transaction.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		return table.Put(key, encoded)
	})
	return errors.Join(updateErr, database.Close())
}

func refCacheKey(owner, repo, gitRef string) []byte {
	return []byte(strings.ToLower(strings.TrimSpace(owner)) + "\x00" + strings.ToLower(strings.TrimSpace(repo)) + "\x00" + strings.TrimSpace(gitRef))
}

func repoCacheKey(owner, repo string) []byte {
	return []byte(strings.ToLower(strings.TrimSpace(owner)) + "\x00" + strings.ToLower(strings.TrimSpace(repo)))
}
