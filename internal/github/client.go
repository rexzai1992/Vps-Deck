package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	apiBase       = "https://api.github.com"
	oauthTokenURL = "https://github.com/login/oauth/access_token"
	maxBody       = 4 << 20
)

// Repo is the trimmed repository view returned to the UI.
type Repo struct {
	FullName      string `json:"full_name"`
	Name          string `json:"name"`
	Owner         string `json:"owner"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Description   string `json:"description"`
	PushedAt      string `json:"pushed_at"`
	HTMLURL       string `json:"html_url"`
}

type User struct {
	Login     string `json:"login"`
	ID        int64  `json:"id"`
	AvatarURL string `json:"avatar_url"`
}

// Client performs the GitHub REST calls VPSDeck needs.
type Client struct {
	http     *http.Client
	api      string
	tokenURL string
}

func newClient(timeout time.Duration) *Client {
	return &Client{
		http:     &http.Client{Timeout: timeout},
		api:      apiBase,
		tokenURL: oauthTokenURL,
	}
}

// ExchangeCode trades an OAuth authorization code for an access token.
func (c *Client) ExchangeCode(ctx context.Context, clientID, clientSecret, code, redirectURI string) (token, scope string, err error) {
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return "", "", errors.New("could not reach GitHub to exchange the authorization code")
	}
	defer response.Body.Close()
	var payload struct {
		AccessToken      string `json:"access_token"`
		Scope            string `json:"scope"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := decodeJSON(response.Body, &payload); err != nil {
		return "", "", err
	}
	if payload.Error != "" {
		return "", "", fmt.Errorf("GitHub rejected the authorization: %s", payload.Error)
	}
	if payload.AccessToken == "" {
		return "", "", errors.New("GitHub did not return an access token")
	}
	return payload.AccessToken, payload.Scope, nil
}

func (c *Client) CurrentUser(ctx context.Context, token string) (User, error) {
	var user User
	if err := c.get(ctx, token, "/user", &user); err != nil {
		return User{}, err
	}
	if user.Login == "" {
		return User{}, errors.New("GitHub did not return the account login")
	}
	return user, nil
}

// ListRepos returns repositories the token can access, optionally filtered by a
// case-insensitive substring of the full name or description.
func (c *Client) ListRepos(ctx context.Context, token, query string) ([]Repo, error) {
	var raw []struct {
		FullName      string `json:"full_name"`
		Name          string `json:"name"`
		Owner         User   `json:"owner"`
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
		Description   string `json:"description"`
		PushedAt      string `json:"pushed_at"`
		HTMLURL       string `json:"html_url"`
	}
	if err := c.get(ctx, token, "/user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator,organization_member", &raw); err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	repos := make([]Repo, 0, len(raw))
	for _, item := range raw {
		if query != "" && !strings.Contains(strings.ToLower(item.FullName), query) && !strings.Contains(strings.ToLower(item.Description), query) {
			continue
		}
		repos = append(repos, Repo{
			FullName:      item.FullName,
			Name:          item.Name,
			Owner:         item.Owner.Login,
			Private:       item.Private,
			DefaultBranch: item.DefaultBranch,
			Description:   item.Description,
			PushedAt:      item.PushedAt,
			HTMLURL:       item.HTMLURL,
		})
	}
	sort.SliceStable(repos, func(i, j int) bool { return repos[i].PushedAt > repos[j].PushedAt })
	return repos, nil
}

func (c *Client) ListBranches(ctx context.Context, token, owner, repo string) ([]string, error) {
	var raw []struct {
		Name string `json:"name"`
	}
	path := fmt.Sprintf("/repos/%s/%s/branches?per_page=100", url.PathEscape(owner), url.PathEscape(repo))
	if err := c.get(ctx, token, path, &raw); err != nil {
		return nil, err
	}
	branches := make([]string, 0, len(raw))
	for _, item := range raw {
		branches = append(branches, item.Name)
	}
	return branches, nil
}

func (c *Client) get(ctx context.Context, token, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "token "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := c.http.Do(request)
	if err != nil {
		return errors.New("could not reach the GitHub API")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return errors.New("GitHub rejected the stored token; reconnect the account")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub API returned status %d", response.StatusCode)
	}
	return decodeJSON(response.Body, target)
}

func decodeJSON(body io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(body, maxBody))
	if err != nil {
		return errors.New("could not read the GitHub response")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("could not parse the GitHub response")
	}
	return nil
}
