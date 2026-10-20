package interop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	DiscoveryLockSchema  = "agentdock/skill-source-lock/v1"
	skillsSearchEndpoint = "https://skills.sh/api/search"
	githubAPIBase        = "https://api.github.com"
	maxDiscoveryBody     = 1 << 20
)

var (
	githubOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	githubRepoPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type DiscoveryClient struct {
	httpClient     HTTPDoer
	searchEndpoint string
	githubAPIBase  string
}

type SearchCandidate struct {
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	Source            string `json:"source"`
	Installs          int    `json:"installs"`
	CandidateSHA256   string `json:"candidate_sha256"`
	Pinnable          bool   `json:"pinnable"`
	InstallAuthorized bool   `json:"install_authorized"`
}

type SourceLock struct {
	Schema            string    `json:"schema"`
	Provider          string    `json:"provider"`
	Repository        string    `json:"repository"`
	Skill             string    `json:"skill,omitempty"`
	Source            SourceRef `json:"source"`
	ReviewRequired    bool      `json:"review_required"`
	InstallAuthorized bool      `json:"install_authorized"`
	NextOwner         string    `json:"next_owner"`
}

type PinResult struct {
	Lock       SourceLock `json:"lock"`
	LockSHA256 string     `json:"lock_sha256"`
	ResolvedAt string     `json:"resolved_at"`
}

func NewDiscoveryClient(client HTTPDoer) *DiscoveryClient {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &DiscoveryClient{
		httpClient:     client,
		searchEndpoint: skillsSearchEndpoint,
		githubAPIBase:  githubAPIBase,
	}
}

func (c *DiscoveryClient) Search(ctx context.Context, query, owner string, limit int) ([]SearchCandidate, error) {
	query = cleanMetadata(query, 200)
	if len(query) < 2 {
		return nil, errors.New("search query must contain at least 2 characters")
	}
	owner = strings.TrimSpace(owner)
	if owner != "" && !githubOwnerPattern.MatchString(owner) {
		return nil, errors.New("owner must be a valid GitHub owner")
	}
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > 20 {
		return nil, errors.New("limit must be between 1 and 20")
	}

	endpoint, err := url.Parse(c.searchEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse fixed discovery endpoint: %w", err)
	}
	params := endpoint.Query()
	params.Set("q", query)
	params.Set("limit", fmt.Sprintf("%d", limit))
	if owner != "" {
		params.Set("owner", strings.ToLower(owner))
	}
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build discovery request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AgentDock-Skill-Discovery/1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query skills registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skills registry returned HTTP %d", resp.StatusCode)
	}

	body, err := readBoundedJSON(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Skills []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Installs int    `json:"installs"`
			Source   string `json:"source"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode skills registry response: %w", err)
	}

	items := make([]SearchCandidate, 0, minInt(limit, len(payload.Skills)))
	for _, raw := range payload.Skills {
		if len(items) >= limit {
			break
		}
		name := cleanMetadata(raw.Name, 200)
		slug := cleanMetadata(raw.ID, 300)
		source := cleanMetadata(raw.Source, 240)
		if name == "" || slug == "" {
			continue
		}
		_, _, pinnable := parsePublicGitHubRepository(source)
		candidate := SearchCandidate{
			Name:              name,
			Slug:              slug,
			Source:            source,
			Installs:          maxInt(raw.Installs, 0),
			Pinnable:          pinnable,
			InstallAuthorized: false,
		}
		candidate.CandidateSHA256 = candidateIdentity(candidate)
		items = append(items, candidate)
	}
	return items, nil
}

func (c *DiscoveryClient) Pin(ctx context.Context, source, skill string) (PinResult, error) {
	owner, repo, ok := parsePublicGitHubRepository(source)
	if !ok {
		return PinResult{}, errors.New("source must identify one public github.com owner/repo repository")
	}
	skill = cleanMetadata(skill, 200)

	apiBase, err := url.Parse(c.githubAPIBase)
	if err != nil {
		return PinResult{}, fmt.Errorf("parse fixed GitHub API endpoint: %w", err)
	}
	apiBase.Path = strings.TrimRight(apiBase.Path, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/commits/HEAD"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase.String(), nil)
	if err != nil {
		return PinResult{}, fmt.Errorf("build GitHub pin request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "AgentDock-Skill-Discovery/1")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return PinResult{}, fmt.Errorf("resolve GitHub HEAD: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return PinResult{}, fmt.Errorf("GitHub API returned HTTP %d while resolving %s/%s", resp.StatusCode, owner, repo)
	}

	body, err := readBoundedJSON(resp.Body)
	if err != nil {
		return PinResult{}, err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(body, &commit); err != nil {
		return PinResult{}, fmt.Errorf("decode GitHub commit response: %w", err)
	}
	sha := strings.ToLower(strings.TrimSpace(commit.SHA))
	if !commitPattern.MatchString(sha) {
		return PinResult{}, errors.New("GitHub API did not return a full 40-hex commit")
	}

	ref, err := ParseSourceRef("github:" + owner + "/" + repo + "@" + sha)
	if err != nil {
		return PinResult{}, fmt.Errorf("normalize pinned source: %w", err)
	}
	lock := SourceLock{
		Schema:            DiscoveryLockSchema,
		Provider:          "github",
		Repository:        owner + "/" + repo,
		Skill:             skill,
		Source:            ref,
		ReviewRequired:    true,
		InstallAuthorized: false,
		NextOwner:         "project-due-diligence",
	}
	digest, err := stableSHA256(lock)
	if err != nil {
		return PinResult{}, fmt.Errorf("hash source lock: %w", err)
	}
	return PinResult{
		Lock:       lock,
		LockSHA256: digest,
		ResolvedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func parsePublicGitHubRepository(raw string) (string, string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", "", false
	}
	if strings.HasPrefix(value, "github:") {
		value = strings.TrimPrefix(value, "github:")
	}
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", "", false
		}
		value = strings.Trim(parsed.Path, "/")
	}
	if strings.ContainsAny(value, "@#") {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) != 2 {
		return "", "", false
	}
	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")
	if !githubOwnerPattern.MatchString(owner) || !githubRepoPattern.MatchString(repo) {
		return "", "", false
	}
	return owner, repo, true
}

func candidateIdentity(candidate SearchCandidate) string {
	identity := struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
		Slug     string `json:"slug"`
		Source   string `json:"source"`
	}{
		Provider: "skills.sh",
		Name:     candidate.Name,
		Slug:     candidate.Slug,
		Source:   candidate.Source,
	}
	digest, _ := stableSHA256(identity)
	return digest
}

func SourceLockSHA256(lock SourceLock) (string, error) {
	return stableSHA256(lock)
}

func stableSHA256(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func readBoundedJSON(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxDiscoveryBody+1))
	if err != nil {
		return nil, fmt.Errorf("read discovery response: %w", err)
	}
	if len(body) > maxDiscoveryBody {
		return nil, errors.New("discovery response exceeded 1 MiB")
	}
	return body, nil
}

func cleanMetadata(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := make([]rune, 0, len(value))
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		runes = append(runes, r)
		if len(runes) >= maxRunes {
			break
		}
	}
	return strings.TrimSpace(string(runes))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
