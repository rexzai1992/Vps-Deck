package github

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/config"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func TestSealOpenRoundTrip(t *testing.T) {
	key := testKey()
	cipher, nonce, err := seal(key, []byte("ghp_secret_token"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := open(key, cipher, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "ghp_secret_token" {
		t.Fatalf("round trip mismatch: %q", plain)
	}
	// A different key must not decrypt.
	other := testKey()
	other[0] ^= 0xff
	if _, err := open(other, cipher, nonce); err == nil {
		t.Fatal("decryption with the wrong key should fail")
	}
	// Tampered ciphertext must fail authentication.
	cipher[0] ^= 0xff
	if _, err := open(key, cipher, nonce); err == nil {
		t.Fatal("tampered ciphertext should fail")
	}
}

func TestAuthorizeURL(t *testing.T) {
	service := NewService(nil, config.GitHubConfig{
		Enabled:     true,
		ClientID:    "abc123",
		CallbackURL: "https://vps.example.com/integrations/github/callback",
		Scopes:      "repo",
	}, testKey())
	got := service.AuthorizeURL("state-xyz")
	for _, want := range []string{"client_id=abc123", "state=state-xyz", "scope=repo", "callback"} {
		if !strings.Contains(got, want) {
			t.Fatalf("authorize URL missing %q: %s", want, got)
		}
	}
}

func TestClientAgainstMockAPI(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user":
			w.Write([]byte(`{"login":"octocat","id":42,"avatar_url":"https://x/y.png"}`))
		case r.URL.Path == "/user/repos":
			w.Write([]byte(`[{"full_name":"octocat/app","name":"app","owner":{"login":"octocat"},"private":true,"default_branch":"main","description":"demo","pushed_at":"2026-01-02T00:00:00Z"},{"full_name":"octocat/site","name":"site","owner":{"login":"octocat"},"private":false,"default_branch":"trunk","description":"website","pushed_at":"2026-01-01T00:00:00Z"}]`))
		case strings.HasPrefix(r.URL.Path, "/repos/octocat/app/branches"):
			w.Write([]byte(`[{"name":"main"},{"name":"dev"}]`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer api.Close()

	client := newClient(5 * time.Second)
	client.api = api.URL

	user, err := client.CurrentUser(t.Context(), "tok")
	if err != nil || user.Login != "octocat" || user.ID != 42 {
		t.Fatalf("current user: %+v err=%v", user, err)
	}

	repos, err := client.ListRepos(t.Context(), "tok", "")
	if err != nil || len(repos) != 2 {
		t.Fatalf("list repos: %d err=%v", len(repos), err)
	}
	if !repos[0].Private || repos[0].FullName != "octocat/app" {
		t.Fatalf("unexpected first repo: %+v", repos[0])
	}
	filtered, err := client.ListRepos(t.Context(), "tok", "site")
	if err != nil || len(filtered) != 1 || filtered[0].Name != "site" {
		t.Fatalf("filtered repos: %+v err=%v", filtered, err)
	}
	branches, err := client.ListBranches(t.Context(), "tok", "octocat", "app")
	if err != nil || len(branches) != 2 || branches[0] != "main" {
		t.Fatalf("branches: %v err=%v", branches, err)
	}
}

func TestExchangeCodeMock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"ghp_abc","scope":"repo","token_type":"bearer"}`))
	}))
	defer server.Close()
	client := newClient(5 * time.Second)
	client.tokenURL = server.URL
	token, scope, err := client.ExchangeCode(t.Context(), "id", "secret", "code", "https://cb")
	if err != nil || token != "ghp_abc" || scope != "repo" {
		t.Fatalf("exchange: token=%q scope=%q err=%v", token, scope, err)
	}
}

func TestServiceTokenStorageAndDecrypt(t *testing.T) {
	db, err := database.Open(t.TempDir() + "/vpsdeck.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.CreateUser(t.Context(), "admin", "hash", "admin"); err != nil {
		t.Fatal(err)
	}

	service := NewService(db, config.GitHubConfig{Enabled: true}, testKey())
	cipher, nonce, err := seal(testKey(), []byte("ghp_stored"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertGitHubAccount(t.Context(), database.GitHubAccount{
		UserID: 1, Login: "octocat", GitHubUserID: 7, Scope: "repo",
		AccessTokenCipher: cipher, TokenNonce: nonce,
	}); err != nil {
		t.Fatal(err)
	}

	if !service.Connected(t.Context(), 1) {
		t.Fatal("account should be reported as connected")
	}
	token, err := service.TokenForUser(t.Context(), 1)
	if err != nil || token != "ghp_stored" {
		t.Fatalf("token for user: %q err=%v", token, err)
	}
	any, err := service.TokenAny(t.Context())
	if err != nil || any != "ghp_stored" {
		t.Fatalf("token any: %q err=%v", any, err)
	}
	if err := service.Disconnect(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if service.Connected(t.Context(), 1) {
		t.Fatal("account should be gone after disconnect")
	}
}
