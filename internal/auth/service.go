package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/database"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid username or password")

type Service struct {
	db              *database.DB
	sessionLifetime time.Duration
	dummyHash       []byte
}

func NewService(db *database.DB, sessionLifetime time.Duration) *Service {
	dummyHash, _ := bcrypt.GenerateFromPassword([]byte("vpsdeck-dummy-password"), bcrypt.DefaultCost)
	return &Service{db: db, sessionLifetime: sessionLifetime, dummyHash: dummyHash}
}

func (s *Service) BootstrapAdmin(username, password string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	count, err := s.db.UserCount(ctx)
	if err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return false, nil
	}

	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return false, errors.New("no administrator exists; set VPSDECK_ADMIN_USERNAME and VPSDECK_ADMIN_PASSWORD for the first start")
	}
	if len(username) < 3 || len(username) > 64 {
		return false, errors.New("administrator username must contain 3 to 64 characters")
	}
	if err := ValidatePassword(password); err != nil {
		return false, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, fmt.Errorf("hash admin password: %w", err)
	}
	if _, err := s.db.CreateUser(ctx, username, string(hash), "admin"); err != nil {
		return false, fmt.Errorf("create admin: %w", err)
	}
	return true, nil
}

// EnsureDemoUser creates a demo/demo account if it does not already exist.
// Only called when VPSDECK_DEMO_MODE=true.
func (s *Service) EnsureDemoUser() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := s.db.UserByUsername(ctx, "demo"); err == nil {
		return nil // already exists
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("demo"), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash demo password: %w", err)
	}
	_, err = s.db.CreateUser(ctx, "demo", string(hash), "admin")
	return err
}

func ValidatePassword(password string) error {
	if len(password) < 12 {
		return errors.New("administrator password must be at least 12 characters")
	}
	var upper, lower, digit bool
	for _, character := range password {
		switch {
		case character >= 'A' && character <= 'Z':
			upper = true
		case character >= 'a' && character <= 'z':
			lower = true
		case character >= '0' && character <= '9':
			digit = true
		}
	}
	if !upper || !lower || !digit {
		return errors.New("administrator password must contain uppercase, lowercase, and numeric characters")
	}
	return nil
}

func (s *Service) Authenticate(ctx context.Context, username, password string) (database.User, error) {
	user, err := s.db.UserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if !database.IsNotFound(err) {
			return database.User{}, fmt.Errorf("read user: %w", err)
		}
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return database.User{}, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return database.User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (s *Service) CreateSession(ctx context.Context, userID int64) (string, time.Time, error) {
	raw, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().UTC().Add(s.sessionLifetime)
	if err := s.db.CreateSession(ctx, hashToken(raw), userID, expiresAt); err != nil {
		return "", time.Time{}, fmt.Errorf("store session: %w", err)
	}
	return raw, expiresAt, nil
}

func (s *Service) UserFromSession(ctx context.Context, rawToken string) (database.User, error) {
	if rawToken == "" {
		return database.User{}, ErrInvalidCredentials
	}
	return s.db.UserBySession(ctx, hashToken(rawToken), time.Now().UTC())
}

func (s *Service) DeleteSession(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	return s.db.DeleteSession(ctx, hashToken(rawToken))
}

// EnableAdvanced marks the session as Advanced Mode for the given duration.
func (s *Service) EnableAdvanced(ctx context.Context, rawToken string, duration time.Duration) (time.Time, error) {
	if rawToken == "" {
		return time.Time{}, ErrInvalidCredentials
	}
	until := time.Now().UTC().Add(duration)
	if err := s.db.SetSessionAdvanced(ctx, hashToken(rawToken), until); err != nil {
		return time.Time{}, err
	}
	return until, nil
}

func (s *Service) DisableAdvanced(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	return s.db.ClearSessionAdvanced(ctx, hashToken(rawToken))
}

// AdvancedStatus reports whether the session currently has Advanced Mode active.
func (s *Service) AdvancedStatus(ctx context.Context, rawToken string) (active bool, until time.Time) {
	if rawToken == "" {
		return false, time.Time{}
	}
	expiry, ok, err := s.db.SessionAdvancedUntil(ctx, hashToken(rawToken), time.Now().UTC())
	if err != nil || !ok {
		return false, time.Time{}
	}
	return true, expiry
}

func NewCSRFToken() (string, error) {
	return randomToken(32)
}

func VerifyCSRF(cookieToken, requestToken string) bool {
	if cookieToken == "" || requestToken == "" || len(cookieToken) != len(requestToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookieToken), []byte(requestToken)) == 1
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type RateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	attempts map[string][]time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:    limit,
		window:   window,
		attempts: make(map[string][]time.Time),
	}
}

func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)
	entries := r.attempts[key]
	kept := entries[:0]
	for _, attempt := range entries {
		if attempt.After(cutoff) {
			kept = append(kept, attempt)
		}
	}
	if len(kept) >= r.limit {
		r.attempts[key] = kept
		return false
	}
	r.attempts[key] = append(kept, now)
	return true
}

func (r *RateLimiter) Reset(key string) {
	r.mu.Lock()
	delete(r.attempts, key)
	r.mu.Unlock()
}
