package bb

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// existingCheckout reports whether dir is the root of a git working tree that has at least one
// remote pointing at one of urls. Remote URLs are compared ignoring protocol, credentials and a
// trailing ".git", so a clone made over SSH matches the repository's HTTPS URL and vice versa.
func existingCheckout(ctx context.Context, git, dir string, urls []string) bool {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}

	// Require dir itself to be the repository root. Otherwise a plain directory nested inside
	// some other checkout would report that checkout's remotes.
	top, err := gitOutput(ctx, git, dir, "rev-parse", "--show-toplevel")
	if err != nil || !sameDir(strings.TrimSpace(top), dir) {
		return false
	}

	want := make(map[string]bool, len(urls))
	for _, u := range urls {
		want[normalizeGitURL(u)] = true
	}

	remotes, err := gitOutput(ctx, git, dir, "remote")
	if err != nil {
		return false
	}
	for remote := range strings.FieldsSeq(remotes) {
		// get-url applies url.<base>.insteadOf rewrites, matching what git would really use.
		configured, err := gitOutput(ctx, git, dir, "remote", "get-url", "--all", remote)
		if err != nil {
			continue
		}
		for u := range strings.FieldsSeq(configured) {
			if want[normalizeGitURL(u)] {
				return true
			}
		}
	}
	return false
}

func gitOutput(ctx context.Context, git, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, git, append([]string{"-C", dir}, args...)...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout // stderr is deliberately discarded: failures here just mean "not a match"
	err := cmd.Run()
	return stdout.String(), err
}

// sameDir reports whether a and b are the same directory, resolving symlinks and relative paths.
func sameDir(a, b string) bool {
	resolve := func(p string) (string, error) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(abs)
	}
	ra, errA := resolve(a)
	rb, errB := resolve(b)
	return errA == nil && errB == nil && ra == rb
}

// normalizeGitURL reduces a git remote URL to host/path so equivalent URLs compare equal:
// https://user@host/a/b.git, ssh://git@host/a/b and git@host:a/b.git all become "host/a/b".
// Local paths (used in tests) are reduced to their cleaned path.
func normalizeGitURL(raw string) string {
	raw = strings.TrimSpace(raw)

	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return strings.ToLower(raw)
		}
		host, path = u.Hostname(), u.Path
	case !strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, ".") && strings.Contains(raw, ":"):
		// scp-like syntax: [user@]host:path
		hostPart, p, _ := strings.Cut(raw, ":")
		if i := strings.LastIndex(hostPart, "@"); i >= 0 {
			hostPart = hostPart[i+1:]
		}
		host, path = hostPart, p
	default:
		path = filepath.ToSlash(filepath.Clean(raw))
	}

	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	return strings.ToLower(strings.Trim(host+"/"+path, "/"))
}

// checkoutRepoSlug finds which repository in workspace the git checkout containing dir is a
// clone of, judging by its remotes ("origin" wins when several match).
func checkoutRepoSlug(ctx context.Context, git, dir, workspace string) (string, bool) {
	remotes, err := gitOutput(ctx, git, dir, "remote")
	if err != nil {
		return "", false
	}

	var found string
	for remote := range strings.FieldsSeq(remotes) {
		configured, err := gitOutput(ctx, git, dir, "remote", "get-url", "--all", remote)
		if err != nil {
			continue
		}
		for u := range strings.FieldsSeq(configured) {
			slug, ok := bitbucketRepoSlug(u, workspace)
			if !ok {
				continue
			}
			if remote == "origin" {
				return slug, true
			}
			if found == "" {
				found = slug
			}
		}
	}
	return found, found != ""
}

// bitbucketRepoSlug extracts the repository slug from a bitbucket.org remote URL, provided it
// belongs to workspace.
func bitbucketRepoSlug(remoteURL, workspace string) (string, bool) {
	parts := strings.Split(normalizeGitURL(remoteURL), "/")
	if len(parts) != 3 || parts[0] != "bitbucket.org" || parts[1] != strings.ToLower(workspace) {
		return "", false
	}
	return parts[2], true
}

// currentBranch returns the branch checked out in dir, failing on a detached HEAD.
func currentBranch(ctx context.Context, git, dir string) (string, error) {
	out, err := gitOutput(ctx, git, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", errors.New("no branch is checked out")
	}
	return strings.TrimSpace(out), nil
}

// lastCommitSubject returns the first line of HEAD's commit message, or "" if there is none.
func lastCommitSubject(ctx context.Context, git, dir string) string {
	out, _ := gitOutput(ctx, git, dir, "log", "-1", "--format=%s")
	return strings.TrimSpace(out)
}
