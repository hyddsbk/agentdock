package interop

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

type SourceKind string

const (
	SourceLocal  SourceKind = "local"
	SourceGitHub SourceKind = "github"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type SourceRef struct {
	Kind      SourceKind `json:"kind"`
	Original  string     `json:"original"`
	Canonical string     `json:"canonical"`
	Immutable bool       `json:"immutable"`
	LocalPath string     `json:"local_path,omitempty"`
	Owner     string     `json:"owner,omitempty"`
	Repo      string     `json:"repo,omitempty"`
	Commit    string     `json:"commit,omitempty"`
	Subpath   string     `json:"subpath,omitempty"`
}

func ParseSourceRef(raw string) (SourceRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SourceRef{}, errors.New("skill source is required")
	}
	if strings.HasPrefix(raw, "github:") {
		return parseGitHubShorthand(raw)
	}
	if filepath.VolumeName(raw) != "" {
		return localSource(raw, raw)
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		if u.Scheme == "https" && strings.EqualFold(u.Host, "github.com") {
			if u.RawQuery != "" || u.Fragment != "" {
				return SourceRef{}, errors.New("github URL must not contain query parameters or fragments")
			}
			return parseGitHubURL(raw, u)
		}
		if u.Scheme != "file" {
			return SourceRef{}, fmt.Errorf("unsupported skill source scheme %q", u.Scheme)
		}
		local := u.Path
		if u.Host != "" {
			local = "//" + u.Host + u.Path
		}
		return localSource(raw, local)
	}
	return localSource(raw, raw)
}

func RequireImmutable(ref SourceRef) error {
	if !ref.Immutable {
		return fmt.Errorf("skill source %q is mutable; an immutable commit-pinned source is required", ref.Original)
	}
	return nil
}

func parseGitHubShorthand(raw string) (SourceRef, error) {
	value := strings.TrimPrefix(raw, "github:")
	var subpath string
	if i := strings.Index(value, "#"); i >= 0 {
		var err error
		subpath, err = cleanSubpath(value[i+1:])
		if err != nil {
			return SourceRef{}, err
		}
		value = value[:i]
	}
	at := strings.LastIndex(value, "@")
	if at <= 0 || at == len(value)-1 {
		return SourceRef{}, errors.New("github source must use github:owner/repo@<40-hex-commit>[#subpath]")
	}
	repoPart, commit := value[:at], strings.ToLower(value[at+1:])
	parts := strings.Split(repoPart, "/")
	if len(parts) != 2 || !validRepoPart(parts[0]) || !validRepoPart(parts[1]) {
		return SourceRef{}, errors.New("github source must contain exactly owner/repo")
	}
	if !commitPattern.MatchString(commit) {
		return SourceRef{}, errors.New("github source must pin a full 40-hex commit")
	}
	return gitHubSource(raw, parts[0], strings.TrimSuffix(parts[1], ".git"), commit, subpath), nil
}

func parseGitHubURL(raw string, u *url.URL) (SourceRef, error) {
	parts := splitPath(u.Path)
	if len(parts) < 4 || parts[2] != "tree" {
		return SourceRef{}, errors.New("github URL must use /owner/repo/tree/<40-hex-commit>[/subpath]")
	}
	owner, repo, commit := parts[0], strings.TrimSuffix(parts[1], ".git"), strings.ToLower(parts[3])
	if !validRepoPart(owner) || !validRepoPart(repo) || !commitPattern.MatchString(commit) {
		return SourceRef{}, errors.New("github URL must contain valid owner/repo and a full 40-hex commit")
	}
	subpath, err := cleanSubpath(strings.Join(parts[4:], "/"))
	if err != nil {
		return SourceRef{}, err
	}
	return gitHubSource(raw, owner, repo, commit, subpath), nil
}

func gitHubSource(original, owner, repo, commit, subpath string) SourceRef {
	canonical := "github:" + owner + "/" + repo + "@" + commit
	if subpath != "" {
		canonical += "#" + subpath
	}
	return SourceRef{
		Kind:      SourceGitHub,
		Original:  original,
		Canonical: canonical,
		Immutable: true,
		Owner:     owner,
		Repo:      repo,
		Commit:    commit,
		Subpath:   subpath,
	}
}

func localSource(original, value string) (SourceRef, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return SourceRef{}, errors.New("local skill source path is empty")
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	return SourceRef{
		Kind:      SourceLocal,
		Original:  original,
		Canonical: "local:" + filepath.ToSlash(clean),
		Immutable: false,
		LocalPath: clean,
	}, nil
}

func cleanSubpath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Trim(value, "/")
	if value == "" {
		return "", nil
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(filepath.FromSlash(clean)) {
		return "", errors.New("github skill subpath must stay within the pinned repository")
	}
	return clean, nil
}

func splitPath(value string) []string {
	raw := strings.Split(strings.Trim(value, "/"), "/")
	out := raw[:0]
	for _, part := range raw {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func validRepoPart(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "\\@#")
}
