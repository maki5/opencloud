package events

import (
	"context"
	"testing"
	"time"

	collaboration "github.com/cs3org/go-cs3apis/cs3/sharing/collaboration/v1beta1"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/guestauth"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
	"github.com/opencloud-eu/reva/v2/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testShareID = "e0123456-7890-abcd-ef01-234567890abc"

func addRecord(t *testing.T, store storage.Storage, shareID string) storage.Record {
	ts := token.NewTokenService()
	tok, err := ts.Generate(shareID)
	require.NoError(t, err)

	rec := storage.Record{
		ShareID:     shareID,
		ShareIDHash: tok.ShareIDHash,
		SecretHash:  tok.SecretHash,
		Expiry:      time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
	}
	require.NoError(t, store.Add(rec))

	return rec
}

func newConsumer(t *testing.T) (*EventConsumer, storage.Storage) {
	store := storage.NewFileStorage(t.TempDir())
	guestAuth := guestauth.NewGuestAuthService(token.NewTokenService(), store)
	consumer, err := NewEventConsumer(nil, GuestAuthService(guestAuth))
	require.NoError(t, err)

	return consumer, store
}

func TestHandleShareRemoved(t *testing.T) {
	svc, store := newConsumer(t)
	rec := addRecord(t, store, testShareID)

	ev := events.ShareRemoved{
		ShareID: &collaboration.ShareId{OpaqueId: testShareID},
	}

	require.NoError(t, svc.handleShareRemoved(context.Background(), ev))

	_, err := store.Get(rec.ShareIDHash)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestHandleShareExpired(t *testing.T) {
	svc, store := newConsumer(t)
	rec := addRecord(t, store, testShareID)

	ev := events.ShareExpired{
		ShareID: &collaboration.ShareId{OpaqueId: testShareID},
	}

	require.NoError(t, svc.handleShareExpired(context.Background(), ev))

	_, err := store.Get(rec.ShareIDHash)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}
