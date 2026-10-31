package bb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
	"read:pullrequest:bitbucket",
	"write:pullrequest:bitbucket",
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

// errNotFound is returned by get for HTTP 404; callers say what wasn't found.
var errNotFound = errors.New("not found (404)")

// get performs an authenticated GET and returns the response on HTTP 200.
// The caller must close the response body.
func get(ctx context.Context, cfg *Config, endpoint string) (*http.Response, error) {
	return do(ctx, cfg, http.MethodGet, endpoint, nil)
}

// do performs an authenticated request, sending body (if non-nil) as JSON, and returns the
// response on HTTP 200 or 201. The caller must close the response body.
func do(ctx context.Context, cfg *Config, method, endpoint string, body any) (*http.Response, error) {
	return doAccepting(ctx, cfg, method, endpoint, body, "application/json")
}

// doAccepting is do with an explicit Accept header, for endpoints that don't return JSON.
func doAccepting(ctx context.Context, cfg *Config, method, endpoint string, body any, accept string) (*http.Response, error) {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(cfg.Email, cfg.Token)
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach Bitbucket: %w", err)
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return resp, nil
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("Bitbucket rejected the email/token (401): check the email and that the token hasn't expired")
	case http.StatusForbidden:
		return nil, fmt.Errorf("token is missing a required scope (403): needs %s", strings.Join(requiredScopes, ", "))
	case http.StatusNotFound:
		return nil, errNotFound
	default:
		if msg := apiErrorMessage(resp.Body); msg != "" {
			return nil, fmt.Errorf("Bitbucket rejected the request (%s): %s", resp.Status, msg)
		}
		return nil, fmt.Errorf("unexpected response from Bitbucket: %s", resp.Status)
	}
}

// apiErrorMessage extracts the human-readable message from a Bitbucket error body, including
// any per-field problems, or returns "" if the body isn't one.
func apiErrorMessage(r io.Reader) string {
	var body struct {
		Error struct {
			Message string              `json:"message"`
			Fields  map[string][]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&body); err != nil {
		return ""
	}

	var msg strings.Builder
	msg.WriteString(body.Error.Message)
	for _, field := range slices.Sorted(maps.Keys(body.Error.Fields)) {
		msg.WriteString(fmt.Sprintf(" [%s: %s]", field, strings.Join(body.Error.Fields[field], "; ")))
	}
	return msg.String()
}

// checkCredentials verifies the email/token pair can access the workspace.
func checkCredentials(ctx context.Context, cfg *Config) error {
	resp, err := get(ctx, cfg, fmt.Sprintf("%s/workspaces/%s", bitbucketAPI, url.PathEscape(cfg.Workspace)))
	if err != nil {
		return notFoundAsWorkspace(err, cfg)
	}
	return resp.Body.Close()
}

func notFoundAsWorkspace(err error, cfg *Config) error {
	if errors.Is(err, errNotFound) {
		return fmt.Errorf("workspace %q not found, or your account can't see it (404)", cfg.Workspace)
	}
	return err
}

// getRepository fetches a single repository from the configured workspace.
func getRepository(ctx context.Context, cfg *Config, slug string) (*Repository, error) {
	resp, err := get(ctx, cfg, fmt.Sprintf("%s/repositories/%s/%s", bitbucketAPI, url.PathEscape(cfg.Workspace), url.PathEscape(slug)))
	if errors.Is(err, errNotFound) {
		return nil, fmt.Errorf("repository %q not found in workspace %q, or your account can't see it (404)", slug, cfg.Workspace)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var repo Repository
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		return nil, fmt.Errorf("decode repository: %w", err)
	}
	return &repo, nil
}

// cloneURLs returns every clone URL the repository advertises, whatever the protocol.
func (r *Repository) cloneURLs() []string {
	urls := make([]string, 0, len(r.Links.Clone))
	for _, l := range r.Links.Clone {
		urls = append(urls, l.Href)
	}
	return urls
}

// cloneURL returns the repository's clone URL for the given protocol ("ssh" or "https").
func (r *Repository) cloneURL(protocol string) (string, error) {
	for _, l := range r.Links.Clone {
		if l.Name == protocol {
			return l.Href, nil
		}
	}
	return "", fmt.Errorf("repository %q has no %s clone URL", r.Slug, protocol)
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
			return nil, notFoundAsWorkspace(err, cfg)
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

// PullRequest is the subset of the Bitbucket pull request resource that bb uses.
type PullRequest struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Author struct {
		DisplayName string `json:"display_name"`
	} `json:"author"`
	Source struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	} `json:"source"`
	UpdatedOn time.Time `json:"updated_on"`
	Links     struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

// URL is the pull request's page in the Bitbucket web UI.
func (p *PullRequest) URL() string { return p.Links.HTML.Href }

// listPullRequests returns the pull requests of a repository in the configured workspace that
// are in any of states (Bitbucket's upper-case names, e.g. "OPEN"), most recently updated first.
func listPullRequests(ctx context.Context, cfg *Config, slug string, states []string) ([]PullRequest, error) {
	q := url.Values{"pagelen": {"50"}, "sort": {"-updated_on"}, "state": states}
	next := fmt.Sprintf("%s/repositories/%s/%s/pullrequests?%s",
		bitbucketAPI, url.PathEscape(cfg.Workspace), url.PathEscape(slug), q.Encode())

	var prs []PullRequest
	for next != "" {
		resp, err := get(ctx, cfg, next)
		if errors.Is(err, errNotFound) {
			return nil, fmt.Errorf("repository %q not found in workspace %q, or your account can't see it (404)", slug, cfg.Workspace)
		}
		if err != nil {
			return nil, err
		}

		var page struct {
			Values []PullRequest `json:"values"`
			Next   string        `json:"next"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode pull requests: %w", err)
		}
		prs = append(prs, page.Values...)
		next = page.Next
	}
	return prs, nil
}

// newPullRequest is what `bb pr create` asks Bitbucket to open.
type newPullRequest struct {
	Title       string
	Description string
	Source      string // source branch
	Destination string // destination branch
	Draft       bool
}

// createPullRequest opens a pull request in a repository of the configured workspace.
func createPullRequest(ctx context.Context, cfg *Config, slug string, pr newPullRequest) (*PullRequest, error) {
	type branchRef struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	}
	ref := func(name string) (r branchRef) { r.Branch.Name = name; return }

	body := struct {
		Title       string    `json:"title"`
		Description string    `json:"description,omitempty"`
		Source      branchRef `json:"source"`
		Destination branchRef `json:"destination"`
		Draft       bool      `json:"draft,omitempty"`
	}{pr.Title, pr.Description, ref(pr.Source), ref(pr.Destination), pr.Draft}

	endpoint := fmt.Sprintf("%s/repositories/%s/%s/pullrequests", bitbucketAPI, url.PathEscape(cfg.Workspace), url.PathEscape(slug))
	resp, err := do(ctx, cfg, http.MethodPost, endpoint, body)
	if errors.Is(err, errNotFound) {
		return nil, fmt.Errorf("repository %q not found in workspace %q, or your account can't see it (404)", slug, cfg.Workspace)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var created PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("decode pull request: %w", err)
	}
	return &created, nil
}

// maxDiffBytes caps how much of a pull request diff is downloaded for previewing.
const maxDiffBytes = 4 << 20

// getPullRequestDiff fetches the unified diff of a pull request. truncated reports that the
// diff was longer than maxDiffBytes and the rest was dropped.
func getPullRequestDiff(ctx context.Context, cfg *Config, slug string, id int) (diff string, truncated bool, err error) {
	endpoint := fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%d/diff",
		bitbucketAPI, url.PathEscape(cfg.Workspace), url.PathEscape(slug), id)
	resp, err := doAccepting(ctx, cfg, http.MethodGet, endpoint, nil, "text/plain, */*")
	if errors.Is(err, errNotFound) {
		return "", false, fmt.Errorf("diff of pull request #%d not found (404)", id)
	}
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDiffBytes+1))
	if err != nil {
		return "", false, fmt.Errorf("read diff: %w", err)
	}
	if len(data) > maxDiffBytes {
		return strings.ToValidUTF8(string(data[:maxDiffBytes]), "�"), true, nil
	}
	return strings.ToValidUTF8(string(data), "�"), false, nil
}

// newComment is a comment to post on a pull request: a general one, or an inline one when Path
// is set (From for a removed line, To for an added or unchanged line).
type newComment struct {
	Body     string
	Path     string
	From, To int
}

// createComment posts a comment on a pull request of a repository in the configured workspace.
func createComment(ctx context.Context, cfg *Config, slug string, id int, c newComment) error {
	type inline struct {
		Path string `json:"path"`
		From int    `json:"from,omitempty"`
		To   int    `json:"to,omitempty"`
	}
	body := struct {
		Content struct {
			Raw string `json:"raw"`
		} `json:"content"`
		Inline *inline `json:"inline,omitempty"`
	}{}
	body.Content.Raw = c.Body
	if c.Path != "" {
		body.Inline = &inline{Path: c.Path, From: c.From, To: c.To}
	}

	endpoint := fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%d/comments",
		bitbucketAPI, url.PathEscape(cfg.Workspace), url.PathEscape(slug), id)
	resp, err := do(ctx, cfg, http.MethodPost, endpoint, body)
	if errors.Is(err, errNotFound) {
		return fmt.Errorf("pull request #%d not found in %s/%s (404)", id, cfg.Workspace, slug)
	}
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
