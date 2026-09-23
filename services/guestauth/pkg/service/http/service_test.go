package http

import (
	"strings"
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
	parts := strings.Split(tok, ".")

	rec := storage.Record{
		ShareID:     testShareID,
		ShareIDHash: parts[1],
		SecretHash:  ts.Hash(parts[2]),
		Expiry:      time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
	}

	return tok, rec
}

func newStorage(t *testing.T) storage.Storage {
	return storage.NewFileStorage(t.TempDir())
}

func newService(t *testing.T, store storage.Storage) *svc {
	s, err := NewService(token.NewTokenService(), store)
	require.NoError(t, err)

	return s
}

func TestVerifyTokenValid(t *testing.T) {
	store := newStorage(t)
	s := newService(t, store)
	tok, rec := newToken(t)
	require.NoError(t, store.Add(rec))

	got, err := s.VerifyToken(tok)
	require.NoError(t, err)
	assert.Equal(t, rec, got)
}

func TestVerifyTokenExpired(t *testing.T) {
	store := newStorage(t)
	s := newService(t, store)
	tok, rec := newToken(t)
	rec.Expiry = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Add(rec))

	_, err := s.VerifyToken(tok)
	assert.ErrorIs(t, err, ErrExpired)
}

func TestVerifyTokenAlreadyRedeemed(t *testing.T) {
	store := newStorage(t)
	s := newService(t, store)
	tok, rec := newToken(t)
	require.NoError(t, store.Add(rec))
	require.NoError(t, store.Redeem(rec.ShareIDHash))

	_, err := s.VerifyToken(tok)
	assert.ErrorIs(t, err, ErrAlreadyRedeemed)
}
