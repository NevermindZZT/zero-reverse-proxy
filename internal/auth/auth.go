package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrTokenExpired = errors.New("token expired")
)

type claims struct {
	Username string `json:"usr"`
	ExpireAt int64  `json:"exp"`
}

// GenerateToken generates an HMAC-signed token
func GenerateToken(username, secret string, ttl time.Duration) (string, error) {
	c := claims{
		Username: username,
		ExpireAt: time.Now().Add(ttl).Unix(),
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	sig := sign(payloadB64, secret)
	return fmt.Sprintf("%s.%s", payloadB64, sig), nil
}

// ValidateToken validates the token and returns the username
func ValidateToken(tokenStr, secret string) (string, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 2 {
		return "", ErrInvalidToken
	}
	payloadB64, sig := parts[0], parts[1]

	expectedSig := sign(payloadB64, secret)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expectedSig)) != 1 {
		return "", ErrInvalidToken
	}

	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", ErrInvalidToken
	}

	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", ErrInvalidToken
	}

	if time.Now().Unix() > c.ExpireAt {
		return "", ErrTokenExpired
	}

	return c.Username, nil
}

func sign(msg, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// CheckPassword verifies password using constant time comparison
func CheckPassword(provided, expected string) bool {
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}
