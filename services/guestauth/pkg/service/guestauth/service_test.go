package guestauth

import (
	"testing"
	"time"

	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testShareID = "e0123456-7890-abcd-ef01-234567890abc"

func newToken(t *testing.T) (string, storage.Record) {
	ts := token.NewTokenService()
	tok, err := ts.Generate(testShareID)
	require.NoError(t, err)

	rec := storage.Record{
		ShareID:     testShareID,
		ShareIDHash: tok.ShareIDHash,
		SecretHash:  tok.SecretHash,
		Expiry:      time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
	}

	return tok.String(), rec
}

func newStorage(t *testing.T) storage.Storage {
	return storage.NewFileStorage(t.TempDir())
}

func TestVerifyTokenValid(t *testing.T) {
	store := newStorage(t)
	s := NewGuestAuthService(token.NewTokenService(), store)
	tok, rec := newToken(t)
	require.NoError(t, store.Add(rec))

	got, err := s.VerifyToken(tok)
	require.NoError(t, err)
	assert.Equal(t, rec, got)
}

func TestCreateTokenPersistsRecord(t *testing.T) {
	store := newStorage(t)
	s := NewGuestAuthService(token.NewTokenService(), store)

	tok, err := s.CreateToken(testShareID)
	require.NoError(t, err)

	rec, err := store.Get(tok.ShareIDHash)
	require.NoError(t, err)
	assert.Equal(t, testShareID, rec.ShareID)
	assert.Equal(t, tok.ShareIDHash, rec.ShareIDHash)
	assert.Equal(t, tok.SecretHash, rec.SecretHash)
	assert.True(t, rec.Expiry.IsZero())
	assert.False(t, rec.Redeemed)
}

func TestVerifyTokenExpired(t *testing.T) {
	store := newStorage(t)
	s := NewGuestAuthService(token.NewTokenService(), store)
	tok, rec := newToken(t)
	rec.Expiry = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Add(rec))

	_, err := s.VerifyToken(tok)
	assert.ErrorIs(t, err, ErrExpired)
}

func TestVerifyTokenAlreadyRedeemed(t *testing.T) {
	store := newStorage(t)
	s := NewGuestAuthService(token.NewTokenService(), store)
	tok, rec := newToken(t)
	require.NoError(t, store.Add(rec))
	require.NoError(t, store.Redeem(rec.ShareIDHash))

	_, err := s.VerifyToken(tok)
	assert.ErrorIs(t, err, ErrAlreadyRedeemed)
}
