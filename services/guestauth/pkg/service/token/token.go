package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	tokenVersion = "v1"
	secretLength = 32
	tokenParts   = 3
)

var ErrInvalidToken = errors.New("invalid token")

type TokenService struct{}

func NewTokenService() *TokenService {
	return &TokenService{}
}

func (s *TokenService) Generate(shareID string) (string, error) {
	secretBytes := make([]byte, secretLength)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", fmt.Errorf("could not generate random secret: %w", err)
	}

	return strings.Join([]string{
		tokenVersion,
		s.Hash(shareID),
		base64.RawURLEncoding.EncodeToString(secretBytes),
	}, "."), nil
}

func (s *TokenService) Verify(tokenString string, storedSecretHash string) error {
	parts := strings.Split(tokenString, ".")
	if len(parts) != tokenParts || parts[0] != tokenVersion || parts[1] == "" || parts[2] == "" {
		return ErrInvalidToken
	}

	if s.Hash(parts[2]) != storedSecretHash {
		return ErrInvalidToken
	}

	return nil
}

func (s *TokenService) ShareIDHash(tokenString string) (string, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != tokenParts || parts[0] != tokenVersion || parts[1] == "" || parts[2] == "" {
		return "", ErrInvalidToken
	}

	return parts[1], nil
}

func (s *TokenService) Hash(str string) string {
	h := sha256.Sum256([]byte(str))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
