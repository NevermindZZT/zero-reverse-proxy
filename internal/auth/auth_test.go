package auth

import (
	"testing"
	"time"
)

func TestAuth_Token(t *testing.T) {
	secret := "test-secret-key-12345"
	user := "admin"

	// Valid token
	token, err := GenerateToken(user, secret, time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	gotUser, err := ValidateToken(token, secret)
	if err != nil {
		t.Fatalf("failed to validate token: %v", err)
	}
	if gotUser != user {
		t.Errorf("expected %s, got %s", user, gotUser)
	}

	// Tampered token
	_, err = ValidateToken(token+"bad", secret)
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}

	// Expired token
	expiredToken, err := GenerateToken(user, secret, -time.Second)
	if err != nil {
		t.Fatalf("failed to generate expired token: %v", err)
	}
	_, err = ValidateToken(expiredToken, secret)
	if err != ErrTokenExpired {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}
