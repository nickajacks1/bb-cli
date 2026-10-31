package bb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

var bitbucketAPI = "https://api.bitbucket.org/2.0" // var so tests can point it at a fake server

// Page where users create Atlassian API tokens (used for Bitbucket Cloud auth).
const apiTokenURL = "https://id.atlassian.com/manage-profile/security/api-tokens"

// Scopes the token needs for the commands bb provides.
var requiredScopes = []string{
	"read:user:bitbucket",
	"read:workspace:bitbucket",
	"read:repository:bitbucket",
	"write:repository:bitbucket",
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Repository is the subset of the Bitbucket repository resource that bb uses.
type Repository struct {
	Slug        string    `json:"slug"`
	FullName    string    `json:"full_name"`
	Description string    `json:"description"`
	IsPrivate   bool      `json:"is_private"`
	Language    string    `json:"language"`
	UpdatedOn   time.Time `json:"updated_on"`
	MainBranch  *struct {
		Name string `json:"name"`
	} `json:"mainbranch"`
	Links struct {
		Clone []struct {
			Name string `json:"name"`
			Href string `json:"href"`
		} `json:"clone"`
	} `json:"links"`
}

// get performs an authenticated GET and returns the response on HTTP 200.
// The caller must close the response body.
func get(ctx context.Context, cfg *Config, endpoint string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(cfg.Email, cfg.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach Bitbucket: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("Bitbucket rejected the email/token (401): check the email and that the token hasn't expired")
	case http.StatusForbidden:
		return nil, fmt.Errorf("token is missing a required scope (403): needs %s", strings.Join(requiredScopes, ", "))
	case http.StatusNotFound:
		return nil, fmt.Errorf("workspace %q not found, or your account can't see it (404)", cfg.Workspace)
	default:
		return nil, fmt.Errorf("unexpected response from Bitbucket: %s", resp.Status)
	}
}

// checkCredentials verifies the email/token pair can access the workspace.
func checkCredentials(ctx context.Context, cfg *Config) error {
	resp, err := get(ctx, cfg, fmt.Sprintf("%s/workspaces/%s", bitbucketAPI, url.PathEscape(cfg.Workspace)))
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// listRepositories returns every repository in the configured workspace, sorted by slug.
// If progress is non-nil it is called after each page with the number of repositories
// fetched so far and the workspace total (0 if Bitbucket didn't report one).
func listRepositories(ctx context.Context, cfg *Config, progress func(fetched, total int)) ([]Repository, error) {
	var repos []Repository

	next := fmt.Sprintf("%s/repositories/%s?pagelen=100", bitbucketAPI, url.PathEscape(cfg.Workspace))
	for next != "" {
		resp, err := get(ctx, cfg, next)
		if err != nil {
			return nil, err
		}

		var page struct {
			Size   int          `json:"size"`
			Values []Repository `json:"values"`
			Next   string       `json:"next"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode repositories: %w", err)
		}

		repos = append(repos, page.Values...)
		next = page.Next
		if progress != nil {
			progress(len(repos), page.Size)
		}
	}

	slices.SortFunc(repos, func(a, b Repository) int { return strings.Compare(a.Slug, b.Slug) })
	return repos, nil
}
