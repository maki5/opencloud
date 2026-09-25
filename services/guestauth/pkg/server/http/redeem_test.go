package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	collaboration "github.com/cs3org/go-cs3apis/cs3/sharing/collaboration/v1beta1"
	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/config"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/guestauth"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/jwt"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/todo/pool"
	cs3mocks "github.com/opencloud-eu/reva/v2/tests/cs3mocks/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testShareID = "e0123456-7890-abcd-ef01-234567890abc"

type gatewayTestSelector struct {
	client gateway.GatewayAPIClient
}

func (s gatewayTestSelector) Next(...pool.Option) (gateway.GatewayAPIClient, error) {
	return s.client, nil
}

func newGatewayMock() *cs3mocks.GatewayAPIClient {
	gwc := &cs3mocks.GatewayAPIClient{}
	gwc.On("Authenticate", mock.Anything, mock.Anything).
		Return(&gateway.AuthenticateResponse{
			Status: &rpc.Status{Code: rpc.Code_CODE_OK},
			Token:  "token",
		}, nil)
	gwc.On("GetShare", mock.Anything, mock.Anything).Return(&collaboration.GetShareResponse{
		Status: &rpc.Status{Code: rpc.Code_CODE_OK},
		Share: &collaboration.Share{
			Id: &collaboration.ShareId{OpaqueId: testShareID},
		},
	}, nil)
	return gwc
}

func newRedeemHandler(t *testing.T, store storage.Storage) http.HandlerFunc {
	t.Helper()
	svc := guestauth.NewGuestAuthService(
		token.NewTokenService(),
		store,
		guestauth.GatewaySelector(gatewayTestSelector{client: newGatewayMock()}),
		guestauth.ServiceAccount(config.ServiceAccount{ServiceAccountID: "sa-id", ServiceAccountSecret: "sa-secret"}),
		guestauth.JWT(jwt.NewJwtService("test-secret", time.Hour)),
	)
	cfg := &config.Config{
		JWT: config.JWT{
			CookieName:   "oc_guest_session",
			CookieSecure: true,
			TTL:          time.Hour,
		},
	}
	return RedeemHandler(log.NopLogger(), svc, cfg)
}

func TestRedeemHandler(t *testing.T) {
	store := storage.NewFileStorage(t.TempDir())
	ts := token.NewTokenService()
	tok, err := ts.Generate(testShareID)
	require.NoError(t, err)
	require.NoError(t, store.Add(storage.Record{
		ShareID:     testShareID,
		ShareIDHash: tok.ShareIDHash,
		SecretHash:  tok.SecretHash,
	}))

	body, err := json.Marshal(RedeemRequest{Token: tok.String()})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	newRedeemHandler(t, store)(rr, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))

	assert.Equal(t, http.StatusOK, rr.Code)

	var cookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "oc_guest_session" {
			cookie = c
		}
	}
	require.NotNil(t, cookie)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, "/", cookie.Path)
	assert.NotEmpty(t, cookie.Value)
}

func TestRedeemHandlerInvalidToken(t *testing.T) {
	store := storage.NewFileStorage(t.TempDir())
	body, err := json.Marshal(RedeemRequest{Token: "not-a-valid-token"})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	newRedeemHandler(t, store)(rr, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
