package bb

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// How long a cached repository list is considered fresh.
const cacheTTL = 14 * 24 * time.Hour

type repoCache struct {
	FetchedAt time.Time    `json:"fetched_at"`
	Repos     []Repository `json:"repos"`
}

func (c *repoCache) expired(now time.Time) bool {
	return now.Sub(c.FetchedAt) > cacheTTL
}

func repoCachePath(workspace string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache dir: %w", err)
	}
	return filepath.Join(dir, "bb", "repos-"+url.PathEscape(workspace)+".json"), nil
}

// loadRepoCache returns the cached repository list, or nil if there is none.
// An unreadable or corrupt cache is treated as absent so it can simply be refetched.
func loadRepoCache(workspace string) *repoCache {
	path, err := repoCachePath(workspace)
	if err != nil {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var c repoCache
	if err := json.Unmarshal(data, &c); err != nil || c.FetchedAt.IsZero() {
		return nil
	}
	return &c
}

func saveRepoCache(workspace string, repos []Repository) error {
	path, err := repoCachePath(workspace)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	data, err := json.Marshal(repoCache{FetchedAt: time.Now(), Repos: repos})
	if err != nil {
		return err
	}

	if err := writeFileAtomic(path, data); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	return nil
}
