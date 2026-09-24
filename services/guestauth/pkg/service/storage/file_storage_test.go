package storage

import (
	"testing"
	"time"

	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRecord(shareID string) Record {
	svc := token.NewTokenService()
	tok, _ := svc.Generate(shareID)

	return Record{
		ShareID:     shareID,
		ShareIDHash: tok.ShareIDHash,
		SecretHash:  tok.SecretHash,
		Expiry:      time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
	}
}

func TestFileStorageAddGet(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStorage(dir)

	rec := newRecord("e0123456-7890-abcd-ef01-234567890abc")
	require.NoError(t, s.Add(rec))

	got, err := s.Get(rec.ShareIDHash)
	require.NoError(t, err)
	assert.Equal(t, rec, got)
}

func TestFileStorageGetMissing(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStorage(dir)

	_, err := s.Get("doesnotexist")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestFileStorageAddOverwrites(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStorage(dir)

	rec := newRecord("e0123456-7890-abcd-ef01-234567890abc")
	require.NoError(t, s.Add(rec))

	rec.SecretHash = "other"
	require.NoError(t, s.Add(rec))

	got, err := s.Get(rec.ShareIDHash)
	require.NoError(t, err)
	assert.Equal(t, "other", got.SecretHash)
}

func TestFileStorageRemove(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStorage(dir)

	rec := newRecord("e0123456-7890-abcd-ef01-234567890abc")
	require.NoError(t, s.Add(rec))

	require.NoError(t, s.Remove(rec.ShareIDHash))

	_, err := s.Get(rec.ShareIDHash)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestFileStorageRemoveMissing(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStorage(dir)

	err := s.Remove("doesnotexist")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestFileStorageRedeem(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStorage(dir)

	rec := newRecord("e0123456-7890-abcd-ef01-234567890abc")
	require.NoError(t, s.Add(rec))

	require.NoError(t, s.Redeem(rec.ShareIDHash))

	got, err := s.Get(rec.ShareIDHash)
	require.NoError(t, err)
	assert.True(t, got.Redeemed)
}

func TestFileStorageRedeemMissing(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStorage(dir)

	err := s.Redeem("doesnotexist")
	assert.ErrorIs(t, err, ErrNotFound)
}
