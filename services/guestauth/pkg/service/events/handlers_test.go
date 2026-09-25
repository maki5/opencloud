package events

import (
	"context"
	"testing"
	"time"

	user "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	collaboration "github.com/cs3org/go-cs3apis/cs3/sharing/collaboration/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	ocEvents "github.com/opencloud-eu/opencloud/pkg/events"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/guestauth"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
	"github.com/opencloud-eu/reva/v2/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	microevents "go-micro.dev/v4/events"
)

const testShareID = "e0123456-7890-abcd-ef01-234567890abc"

type testBus struct {
	published []any
}

func (tb *testBus) Publish(_ string, ev any, _ ...microevents.PublishOption) error {
	tb.published = append(tb.published, ev)
	return nil
}

func (tb *testBus) Consume(_ string, _ ...microevents.ConsumeOption) (<-chan microevents.Event, error) {
	return nil, nil
}

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

func newConsumer(t *testing.T) (*EventConsumer, storage.Storage, *testBus) {
	t.Helper()
	store := storage.NewFileStorage(t.TempDir())
	guestAuth := guestauth.NewGuestAuthService(token.NewTokenService(), store)
	bus := &testBus{}
	consumer, err := NewEventConsumer(bus, GuestAuthService(guestAuth))
	require.NoError(t, err)

	return consumer, store, bus
}

func TestHandleShareCreated(t *testing.T) {
	svc, store, bus := newConsumer(t)

	ev := events.ShareCreated{
		ShareID:       &collaboration.ShareId{OpaqueId: testShareID},
		Sharer:        &user.UserId{OpaqueId: "sharer"},
		ItemID:        &provider.ResourceId{StorageId: "storage", OpaqueId: "item"},
		ResourceName:  "resource",
		GranteeUserID: &user.UserId{OpaqueId: "guest", Type: user.UserType_USER_TYPE_GUEST},
	}

	require.NoError(t, svc.handleShareCreated(context.Background(), ev))

	rec, err := store.Get(token.Hash(testShareID))
	require.NoError(t, err)
	assert.Equal(t, testShareID, rec.ShareID)

	require.Len(t, bus.published, 1)
	published, ok := bus.published[0].(ocEvents.GuestTokenCreated)
	require.True(t, ok)
	assert.Equal(t, testShareID, published.ShareID.GetOpaqueId())
	assert.Equal(t, ev.Sharer, published.Sharer)
	assert.Equal(t, ev.ItemID, published.ItemID)
	assert.Equal(t, ev.ResourceName, published.ResourceName)

	parsed, err := token.NewTokenService().Parse(published.Token)
	require.NoError(t, err)
	assert.Equal(t, token.Hash(testShareID), parsed.ShareIDHash)
	assert.Equal(t, rec.SecretHash, parsed.SecretHash)
}

func TestHandleShareRemoved(t *testing.T) {
	svc, store, _ := newConsumer(t)
	rec := addRecord(t, store, testShareID)

	ev := events.ShareRemoved{
		ShareID: &collaboration.ShareId{OpaqueId: testShareID},
	}

	require.NoError(t, svc.handleShareRemoved(context.Background(), ev))

	_, err := store.Get(rec.ShareIDHash)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestHandleShareExpired(t *testing.T) {
	svc, store, _ := newConsumer(t)
	rec := addRecord(t, store, testShareID)

	ev := events.ShareExpired{
		ShareID: &collaboration.ShareId{OpaqueId: testShareID},
	}

	require.NoError(t, svc.handleShareExpired(context.Background(), ev))

	_, err := store.Get(rec.ShareIDHash)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}
