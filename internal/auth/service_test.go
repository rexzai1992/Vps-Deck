package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/database"
)

func TestBootstrapAuthenticateAndSession(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	service := NewService(db, time.Hour)
	created, err := service.BootstrapAdmin("admin", "StrongPassword123")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected administrator to be created")
	}

	user, err := service.Authenticate(context.Background(), "admin", "StrongPassword123")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	token, _, err := service.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	resolved, err := service.UserFromSession(context.Background(), token)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if resolved.Username != "admin" {
		t.Fatalf("unexpected session user: %q", resolved.Username)
	}
	if err := service.DeleteSession(context.Background(), token); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := service.UserFromSession(context.Background(), token); err == nil {
		t.Fatal("deleted session was still accepted")
	}
}

func TestValidatePassword(t *testing.T) {
	for _, test := range []struct {
		password string
		valid    bool
	}{
		{"short", false},
		{"alllowercase12345", false},
		{"ALLUPPERCASE12345", false},
		{"NoNumbersAllowed", false},
		{"StrongPassword123", true},
	} {
		err := ValidatePassword(test.password)
		if test.valid && err != nil {
			t.Errorf("%q should be valid: %v", test.password, err)
		}
		if !test.valid && err == nil {
			t.Errorf("%q should be invalid", test.password)
		}
	}
}

func TestCSRF(t *testing.T) {
	token, err := NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyCSRF(token, token) {
		t.Fatal("matching token was rejected")
	}
	if VerifyCSRF(token, token+"x") || VerifyCSRF(token, "") {
		t.Fatal("invalid token was accepted")
	}
}
