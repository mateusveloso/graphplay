// Package github is the deterministic side of the pipeline: fetching things.
package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Issue is what the graph needs to know about an issue.
type Issue struct {
	Owner  string   `json:"owner"`
	Repo   string   `json:"repo"`
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels"`
	URL    string   `json:"url"`
}

// Repository is the contract the graph depends on. Tests implement it with a fake.
type Repository interface {
	Issue(ctx context.Context, owner, repo string, number int) (Issue, error)
	Root(ctx context.Context, owner, repo string) ([]string, error)
	File(ctx context.Context, owner, repo, path string) (string, error)
	Search(ctx context.Context, owner, repo, query string) ([]string, error)
}

// Client talks to api.github.com with an optional token.
type Client struct {
	http  *http.Client
	base  string
	token string
}

func NewClient(token string) *Client {
	return &Client{
		http:  &http.Client{Timeout: 20 * time.Second},
		base:  "https://api.github.com",
		token: token,
	}
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "graph-issue-triage")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("github: %s -> %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Issue(ctx context.Context, owner, repo string, number int) (Issue, error) {
	var raw struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		URL    string `json:"html_url"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.get(ctx, path, nil, &raw); err != nil {
		return Issue{}, err
	}
	issue := Issue{Owner: owner, Repo: repo, Number: number, Title: raw.Title, Body: raw.Body, URL: raw.URL}
	for _, l := range raw.Labels {
		issue.Labels = append(issue.Labels, l.Name)
	}
	return issue, nil
}

type entry struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

func (c *Client) Root(ctx context.Context, owner, repo string) ([]string, error) {
	var entries []entry
	if err := c.get(ctx, fmt.Sprintf("/repos/%s/%s/contents/", owner, repo), nil, &entries); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type == "dir" {
			paths = append(paths, e.Path+"/")
		} else {
			paths = append(paths, e.Path)
		}
	}
	return paths, nil
}

func (c *Client) File(ctx context.Context, owner, repo, path string) (string, error) {
	var raw json.RawMessage
	if err := c.get(ctx, fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, path), nil, &raw); err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") { // a directory: return its listing
		var entries []entry
		if err := json.Unmarshal(raw, &entries); err != nil {
			return "", err
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Path
		}
		return strings.Join(names, "\n"), nil
	}
	var e entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return "", err
	}
	if e.Encoding != "base64" {
		return "", fmt.Errorf("github: %s has unsupported encoding %q", path, e.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(e.Content, "\n", ""))
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func (c *Client) Search(ctx context.Context, owner, repo, query string) ([]string, error) {
	var raw struct {
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
	}
	q := url.Values{"q": {query + " repo:" + owner + "/" + repo}, "per_page": {"10"}}
	if err := c.get(ctx, "/search/code", q, &raw); err != nil {
		return nil, err
	}
	paths := make([]string, len(raw.Items))
	for i, it := range raw.Items {
		paths[i] = it.Path
	}
	return paths, nil
}

// ParseRef turns "owner/repo#123" into its parts.
func ParseRef(ref string) (owner, repo string, number int, err error) {
	full, num, ok := strings.Cut(ref, "#")
	if !ok {
		return "", "", 0, fmt.Errorf("issue ref must look like owner/repo#123, got %q", ref)
	}
	owner, repo, ok = strings.Cut(full, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", 0, fmt.Errorf("issue ref must look like owner/repo#123, got %q", ref)
	}
	number, err = strconv.Atoi(num)
	if err != nil || number <= 0 {
		return "", "", 0, fmt.Errorf("issue ref must look like owner/repo#123, got %q", ref)
	}
	return owner, repo, number, nil
}
