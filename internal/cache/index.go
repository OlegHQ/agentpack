package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/OlegHQ/agentpack/internal/database"
	"github.com/OlegHQ/agentpack/internal/lockfile"
)

var (
	entriesBucket = []byte("cache_entries")
	aliasesBucket = []byte("aliases")
)

type EntryRecord struct {
	Kind          lockfile.PackageKind `json:"kind"`
	SourceURL     string               `json:"source_url"`
	Owner         string               `json:"owner"`
	Repo          string               `json:"repo"`
	Path          string               `json:"path"`
	Commit        string               `json:"commit"`
	FetchedAtUnix int64                `json:"fetched_at_unix"`
}

func UpsertEntry(cacheKey string, record EntryRecord, aliases []string) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("serialize cache entry: %w", err)
	}
	database, err := database.OpenCache(false)
	if err != nil {
		return err
	}
	updateErr := database.Update(func(transaction *bolt.Tx) error {
		entries, err := transaction.CreateBucketIfNotExists(entriesBucket)
		if err != nil {
			return fmt.Errorf("open cache entries bucket: %w", err)
		}
		if err := entries.Put([]byte(cacheKey), encoded); err != nil {
			return fmt.Errorf("write cache entry: %w", err)
		}
		if len(aliases) == 0 {
			return nil
		}
		aliasBucket, err := transaction.CreateBucketIfNotExists(aliasesBucket)
		if err != nil {
			return fmt.Errorf("open aliases bucket: %w", err)
		}
		for _, alias := range aliases {
			key := strings.ToLower(strings.TrimSpace(alias))
			if key == "" {
				continue
			}
			if err := aliasBucket.Put([]byte(key), []byte(cacheKey)); err != nil {
				return fmt.Errorf("write cache alias %q: %w", key, err)
			}
		}
		return nil
	})
	closeErr := database.Close()
	if updateErr != nil {
		if closeErr != nil {
			return errors.Join(fmt.Errorf("update cache database: %w", updateErr), fmt.Errorf("close cache database: %w", closeErr))
		}
		return fmt.Errorf("update cache database: %w", updateErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close cache database: %w", closeErr)
	}
	return nil
}

func LookupAlias(alias string) (string, bool, error) {
	var cacheKey string
	err := viewDatabase(func(transaction *bolt.Tx) error {
		bucket := transaction.Bucket(aliasesBucket)
		if bucket == nil {
			return nil
		}
		value := bucket.Get([]byte(strings.ToLower(strings.TrimSpace(alias))))
		if value != nil {
			cacheKey = string(value)
		}
		return nil
	})
	return cacheKey, cacheKey != "", err
}

func GetEntry(cacheKey string) (EntryRecord, bool, error) {
	var record EntryRecord
	found := false
	err := viewDatabase(func(transaction *bolt.Tx) error {
		bucket := transaction.Bucket(entriesBucket)
		if bucket == nil {
			return nil
		}
		value := bucket.Get([]byte(cacheKey))
		if value == nil {
			return nil
		}
		if err := json.Unmarshal(value, &record); err != nil {
			return fmt.Errorf("deserialize cache entry %q: %w", cacheKey, err)
		}
		found = true
		return nil
	})
	return record, found, err
}

func ListKeys() ([]string, error) {
	var keys []string
	err := viewDatabase(func(transaction *bolt.Tx) error {
		bucket := transaction.Bucket(entriesBucket)
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(key, _ []byte) error {
			keys = append(keys, string(key))
			return nil
		})
	})
	return keys, err
}

func AliasesForGitHubEntry(owner, repo, inRepoPath, packageName string) []string {
	owner = strings.ToLower(owner)
	repo = strings.ToLower(repo)
	path := strings.Trim(inRepoPath, "/")
	aliases := make([]string, 0, 2)
	if path == "" {
		aliases = append(aliases, owner+"/"+repo)
	} else {
		aliases = append(aliases, owner+"/"+repo+"/"+path)
	}
	if name := strings.ToLower(strings.TrimSpace(packageName)); name != "" {
		aliases = append(aliases, name)
	}
	return aliases
}

func viewDatabase(view func(*bolt.Tx) error) error {
	database, err := database.OpenCache(true)
	if err != nil {
		return err
	}
	if database == nil {
		return nil
	}
	viewErr := database.View(view)
	closeErr := database.Close()
	if viewErr != nil {
		if closeErr != nil {
			return errors.Join(fmt.Errorf("read cache database: %w", viewErr), fmt.Errorf("close cache database: %w", closeErr))
		}
		return fmt.Errorf("read cache database: %w", viewErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close cache database: %w", closeErr)
	}
	return nil
}
