package repohost

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// ParseURL classifies a repo-host URL and returns its provider,
// owner, repository, and (when applicable) the kind/number/SHA.
//
// Supported shapes:
//
//	https://github.com/owner/repo
//	https://github.com/owner/repo/pull/42
//	https://github.com/owner/repo/issues/7
//	https://github.com/owner/repo/commit/<sha>
//	https://gitlab.com/owner/repo
//	https://gitlab.com/grp/sub/repo                       (sub-groups)
//	https://gitlab.com/owner/repo/-/merge_requests/12
//	https://gitlab.com/owner/repo/-/issues/7
//	https://gitlab.com/owner/repo/-/commit/<sha>
//	https://gitee.com/owner/repo
//	https://gitee.com/owner/repo/pulls/1                  (plural)
//	https://gitee.com/owner/repo/issues/I12ABC            (alphanum ID)
//	https://gitee.com/owner/repo/commit/<sha>
//	https://gitea.com/owner/repo
//	https://gitea.example.com/owner/repo/pulls/1          (plural)
//	https://gitea.example.com/owner/repo/issues/2
//	https://gitea.example.com/owner/repo/commit/<sha>
//	https://bitbucket.org/owner/repo
//	https://bitbucket.org/owner/repo/pull-requests/3      (hyphen)
//	https://bitbucket.org/owner/repo/issues/2
//	https://bitbucket.org/owner/repo/commits/<sha>        (plural)
//
// Self-hosted hosts that don't match a known prefix or path shape
// return an error; callers may pass [Config.Provider] explicitly to
// override the heuristic.
func ParseURL(raw string) (ParsedURL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return ParsedURL{}, err
	}
	if u.Scheme == "" || u.Host == "" {
		return ParsedURL{}, errors.New("repohost: URL must have scheme and host")
	}
	host := strings.ToLower(u.Host)
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return ParsedURL{}, errors.New("repohost: URL must have a path")
	}
	base := u.Scheme + "://" + u.Host

	// Detection priority:
	//   1. /-/ separator (GitLab unique).
	//   2. gitee.com / gitee. host (must precede gitea check, else
	//      "gitea." substring would otherwise match "gitee.").
	//   3. bitbucket.org / bitbucket. host.
	//   4. gitea.com / gitea. host substring.
	//   5. github.com / github. host prefix.
	switch {
	case strings.Contains(path, "/-/") || host == "gitlab.com" || hasHostPrefix(host, "gitlab."):
		return parseGitLab(base, path)
	case host == "gitee.com" || hasHostPrefix(host, "gitee."):
		return parseGitee(base, path)
	case host == "bitbucket.org" || hasHostPrefix(host, "bitbucket."):
		return parseBitbucket(base, path)
	case host == "gitea.com" || strings.Contains(host, "gitea."):
		return parseGitea(base, path)
	case host == "github.com" || hasHostPrefix(host, "github."):
		return parseGitHub(base, path)
	}

	// Last-chance heuristic: a `<owner>/<repo>/pulls/N` segment is
	// distinctive to Gitea (vs GitHub's singular `pull`). This lets
	// vanity-domain Gitea installations parse without an explicit
	// provider override.
	if hasPathSegment(path, "pulls") {
		return parseGitea(base, path)
	}
	return ParsedURL{}, errors.New("repohost: unrecognized URL")
}

func hasHostPrefix(host, prefix string) bool {
	return strings.HasPrefix(host, prefix)
}

func hasPathSegment(path, seg string) bool {
	for _, p := range strings.Split(path, "/") {
		if p == seg {
			return true
		}
	}
	return false
}

func parseGitHub(base, path string) (ParsedURL, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ParsedURL{}, errors.New("repohost: github URL must include owner/repo")
	}
	out := ParsedURL{Provider: "github", BaseURL: base, Owner: parts[0], Repo: parts[1]}
	if len(parts) == 2 {
		out.Kind = "repo"
		return out, nil
	}
	if len(parts) >= 4 {
		switch parts[2] {
		case "pull":
			out.Kind = "pull"
			n, err := strconv.Atoi(parts[3])
			if err != nil {
				return out, errors.New("repohost: github pull URL has non-numeric number")
			}
			out.Number = n
			return out, nil
		case "issues":
			out.Kind = "issue"
			n, err := strconv.Atoi(parts[3])
			if err != nil {
				return out, errors.New("repohost: github issue URL has non-numeric number")
			}
			out.Number = n
			return out, nil
		case "commit":
			out.Kind = "commit"
			out.SHA = parts[3]
			return out, nil
		}
	}
	out.Kind = "repo"
	return out, nil
}

func parseGitLab(base, path string) (ParsedURL, error) {
	out := ParsedURL{Provider: "gitlab", BaseURL: base}
	// Split on `/-/` to isolate sub-group repo path from the kind+id.
	idx := strings.Index(path, "/-/")
	var repoPath, rest string
	if idx >= 0 {
		repoPath = path[:idx]
		rest = strings.TrimPrefix(path[idx+3:], "")
	} else {
		repoPath = path
	}
	owner, repo, ok := splitOwnerRepo(repoPath)
	if !ok {
		return out, errors.New("repohost: gitlab URL must include owner/repo")
	}
	out.Owner = owner
	out.Repo = repo
	if rest == "" {
		out.Kind = "repo"
		return out, nil
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return out, errors.New("repohost: gitlab URL has incomplete kind")
	}
	switch parts[0] {
	case "merge_requests":
		out.Kind = "pull"
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			return out, errors.New("repohost: gitlab merge_requests URL has non-numeric number")
		}
		out.Number = n
	case "issues":
		out.Kind = "issue"
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			return out, errors.New("repohost: gitlab issues URL has non-numeric number")
		}
		out.Number = n
	case "commit":
		out.Kind = "commit"
		out.SHA = parts[1]
	default:
		return out, errors.New("repohost: gitlab URL kind not recognized")
	}
	return out, nil
}

// splitOwnerRepo splits a slash-joined path into (owner, repo). For
// GitLab sub-groups the owner is everything before the LAST slash.
func splitOwnerRepo(path string) (string, string, bool) {
	if path == "" {
		return "", "", false
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	repo := parts[len(parts)-1]
	owner := strings.Join(parts[:len(parts)-1], "/")
	return owner, repo, true
}

func parseGitea(base, path string) (ParsedURL, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ParsedURL{}, errors.New("repohost: gitea URL must include owner/repo")
	}
	out := ParsedURL{Provider: "gitea", BaseURL: base, Owner: parts[0], Repo: parts[1]}
	if len(parts) == 2 {
		out.Kind = "repo"
		return out, nil
	}
	if len(parts) >= 4 {
		switch parts[2] {
		case "pulls":
			out.Kind = "pull"
			n, err := strconv.Atoi(parts[3])
			if err != nil {
				return out, errors.New("repohost: gitea pulls URL has non-numeric number")
			}
			out.Number = n
			return out, nil
		case "issues":
			out.Kind = "issue"
			n, err := strconv.Atoi(parts[3])
			if err != nil {
				return out, errors.New("repohost: gitea issues URL has non-numeric number")
			}
			out.Number = n
			return out, nil
		case "commit":
			out.Kind = "commit"
			out.SHA = parts[3]
			return out, nil
		}
	}
	out.Kind = "repo"
	return out, nil
}

// parseGitee mirrors parseGitea — Gitee's URL shapes are nearly
// identical (plural "pulls", "issues", singular "commit") — except
// Gitee's issue numbers can be alphanumeric (e.g. "I12ABC"). When the
// number isn't purely numeric, we leave Number=0 and stash the raw
// id into a synthetic SHA-style field via the catch-all (no
// dedicated alphanum field on ParsedURL). Adopters that need the
// alphanumeric form should consult the URL itself.
func parseGitee(base, path string) (ParsedURL, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ParsedURL{}, errors.New("repohost: gitee URL must include owner/repo")
	}
	out := ParsedURL{Provider: "gitee", BaseURL: base, Owner: parts[0], Repo: parts[1]}
	if len(parts) == 2 {
		out.Kind = "repo"
		return out, nil
	}
	if len(parts) >= 4 {
		switch parts[2] {
		case "pulls":
			out.Kind = "pull"
			n, err := strconv.Atoi(parts[3])
			if err != nil {
				return out, errors.New("repohost: gitee pulls URL has non-numeric number")
			}
			out.Number = n
			return out, nil
		case "issues":
			out.Kind = "issue"
			// Gitee issue ids may be alphanumeric (e.g. I12ABC). Try
			// numeric parse first; fall back to leaving Number=0 when
			// the id is not purely numeric — the caller can read the
			// raw id from the URL path.
			if n, err := strconv.Atoi(parts[3]); err == nil {
				out.Number = n
			}
			return out, nil
		case "commit":
			out.Kind = "commit"
			out.SHA = parts[3]
			return out, nil
		}
	}
	out.Kind = "repo"
	return out, nil
}

func parseBitbucket(base, path string) (ParsedURL, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ParsedURL{}, errors.New("repohost: bitbucket URL must include owner/repo")
	}
	out := ParsedURL{Provider: "bitbucket", BaseURL: base, Owner: parts[0], Repo: parts[1]}
	if len(parts) == 2 {
		out.Kind = "repo"
		return out, nil
	}
	if len(parts) >= 4 {
		switch parts[2] {
		case "pull-requests":
			out.Kind = "pull"
			n, err := strconv.Atoi(parts[3])
			if err != nil {
				return out, errors.New("repohost: bitbucket pull-requests URL has non-numeric number")
			}
			out.Number = n
			return out, nil
		case "issues":
			out.Kind = "issue"
			n, err := strconv.Atoi(parts[3])
			if err != nil {
				return out, errors.New("repohost: bitbucket issues URL has non-numeric number")
			}
			out.Number = n
			return out, nil
		case "commits":
			out.Kind = "commit"
			out.SHA = parts[3]
			return out, nil
		}
	}
	out.Kind = "repo"
	return out, nil
}
