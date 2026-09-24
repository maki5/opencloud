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

type Token struct {
	ShareIDHash string
	SecretHash  string
	secret      string
}

func (t *Token) String() string {
	return strings.Join([]string{tokenVersion, t.ShareIDHash, t.secret}, ".")
}

type TokenService struct{}

func NewTokenService() *TokenService {
	return &TokenService{}
}

func (s *TokenService) Generate(shareID string) (*Token, error) {
	secretBytes := make([]byte, secretLength)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("could not generate random secret: %w", err)
	}

	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	return &Token{
		ShareIDHash: Hash(shareID),
		SecretHash:  Hash(secret),
		secret:      secret,
	}, nil
}

func (s *TokenService) Parse(encoded string) (*Token, error) {
	parts := strings.Split(encoded, ".")
	if len(parts) != tokenParts || parts[0] != tokenVersion || parts[1] == "" || parts[2] == "" {
		return nil, ErrInvalidToken
	}

	return &Token{
		ShareIDHash: parts[1],
		SecretHash:  Hash(parts[2]),
		secret:      parts[2],
	}, nil
}

func (s *TokenService) Verify(candidate Token, storedSecretHash string) error {
	secretHash := Hash(candidate.secret)
	if candidate.ShareIDHash == "" || candidate.secret == "" || candidate.SecretHash != secretHash || secretHash != storedSecretHash {
		return ErrInvalidToken
	}

	return nil
}

func Hash(value string) string {
	h := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
