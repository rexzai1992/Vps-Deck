// Package github implements VPSDeck's GitHub OAuth App integration: connecting an
// account, listing the user's repositories and branches, and supplying an access
// token (encrypted at rest) for authenticated clones and fetches.
package github

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/config"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

type Service struct {
	db     *database.DB
	cfg    config.GitHubConfig
	key    []byte
	client *Client
}

func NewService(db *database.DB, cfg config.GitHubConfig, key []byte) *Service {
	return &Service{
		db:     db,
		cfg:    cfg,
		key:    key,
		client: newClient(15 * time.Second),
	}
}

func (s *Service) Enabled() bool { return s.cfg.Enabled }

// AuthorizeURL builds the GitHub consent URL for the given anti-CSRF state.
func (s *Service) AuthorizeURL(state string) string {
	query := url.Values{
		"client_id":    {s.cfg.ClientID},
		"redirect_uri": {s.cfg.CallbackURL},
		"scope":        {s.cfg.Scopes},
		"state":        {state},
		"allow_signup": {"false"},
	}
	return "https://github.com/login/oauth/authorize?" + query.Encode()
}

// HandleCallback exchanges the code, fetches the account identity, and stores the
// access token encrypted under the configured key.
func (s *Service) HandleCallback(ctx context.Context, userID int64, code string) (database.GitHubAccount, error) {
	if !s.cfg.Enabled {
		return database.GitHubAccount{}, errors.New("the GitHub integration is disabled")
	}
	if strings.TrimSpace(code) == "" {
		return database.GitHubAccount{}, errors.New("missing authorization code")
	}
	token, scope, err := s.client.ExchangeCode(ctx, s.cfg.ClientID, s.cfg.ClientSecret, code, s.cfg.CallbackURL)
	if err != nil {
		return database.GitHubAccount{}, err
	}
	user, err := s.client.CurrentUser(ctx, token)
	if err != nil {
		return database.GitHubAccount{}, err
	}
	cipher, nonce, err := seal(s.key, []byte(token))
	if err != nil {
		return database.GitHubAccount{}, errors.New("could not encrypt the access token")
	}
	account := database.GitHubAccount{
		UserID:            userID,
		Login:             user.Login,
		GitHubUserID:      user.ID,
		AvatarURL:         user.AvatarURL,
		Scope:             scope,
		AccessTokenCipher: cipher,
		TokenNonce:        nonce,
	}
	if err := s.db.UpsertGitHubAccount(ctx, account); err != nil {
		return database.GitHubAccount{}, err
	}
	return account, nil
}

func (s *Service) Disconnect(ctx context.Context, userID int64) error {
	return s.db.DeleteGitHubAccount(ctx, userID)
}

func (s *Service) Account(ctx context.Context, userID int64) (database.GitHubAccount, bool) {
	account, err := s.db.GitHubAccountByUser(ctx, userID)
	if err != nil {
		return database.GitHubAccount{}, false
	}
	return account, true
}

func (s *Service) Connected(ctx context.Context, userID int64) bool {
	_, ok := s.Account(ctx, userID)
	return ok
}

func (s *Service) Repositories(ctx context.Context, userID int64, query string) ([]Repo, error) {
	token, err := s.TokenForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.client.ListRepos(ctx, token, query)
}

func (s *Service) Branches(ctx context.Context, userID int64, owner, repo string) ([]string, error) {
	token, err := s.TokenForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.client.ListBranches(ctx, token, owner, repo)
}

// TokenForUser returns the decrypted access token for a specific user.
func (s *Service) TokenForUser(ctx context.Context, userID int64) (string, error) {
	account, err := s.db.GitHubAccountByUser(ctx, userID)
	if err != nil {
		return "", errors.New("no GitHub account is connected")
	}
	return s.decrypt(account)
}

// TokenAny returns the decrypted token of the most recently connected account.
// It backs authenticated deploys in the single-admin MVP.
func (s *Service) TokenAny(ctx context.Context) (string, error) {
	account, err := s.db.GitHubAccountAny(ctx)
	if err != nil {
		return "", errors.New("no GitHub account is connected")
	}
	return s.decrypt(account)
}

func (s *Service) decrypt(account database.GitHubAccount) (string, error) {
	token, err := open(s.key, account.AccessTokenCipher, account.TokenNonce)
	if err != nil {
		return "", errors.New("could not decrypt the stored GitHub token")
	}
	return string(token), nil
}
