package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/config"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/guestauth"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/guestauth/mocks"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newRedeemHandler(t *testing.T, svc guestauth.GuestAuth) http.HandlerFunc {
	t.Helper()
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
	svcMock := mocks.NewGuestAuth(t)
	svcMock.On("Redeem", mock.Anything, "valid-token").Return("session-token", nil)

	body, err := json.Marshal(RedeemRequest{Token: "valid-token"})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	newRedeemHandler(t, svcMock)(rr, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))

	assert.Equal(t, http.StatusOK, rr.Code)

	var cookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "oc_guest_session" {
			cookie = c
		}
	}
	require.NotNil(t, cookie)
	assert.Equal(t, "session-token", cookie.Value)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, "/", cookie.Path)
}

func TestRedeemHandlerErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "already redeemed", err: guestauth.ErrAlreadyRedeemed, wantStatus: http.StatusConflict},
		{name: "token expired", err: guestauth.ErrExpired, wantStatus: http.StatusGone},
		{name: "token not found", err: storage.ErrNotFound, wantStatus: http.StatusNotFound},
		{name: "invalid token", err: token.ErrInvalidToken, wantStatus: http.StatusUnauthorized},
		{name: "share not found", err: guestauth.ErrShareNotFound, wantStatus: http.StatusNotFound},
		{name: "share expired", err: guestauth.ErrShareExpired, wantStatus: http.StatusGone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svcMock := mocks.NewGuestAuth(t)
			svcMock.On("Redeem", mock.Anything, "token").Return("", tt.err)

			body, err := json.Marshal(RedeemRequest{Token: "token"})
			require.NoError(t, err)

			rr := httptest.NewRecorder()
			newRedeemHandler(t, svcMock)(rr, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))

			assert.Equal(t, tt.wantStatus, rr.Code)
		})
	}
}

func TestRedeemHandlerMalformedBody(t *testing.T) {
	svcMock := mocks.NewGuestAuth(t)

	rr := httptest.NewRecorder()
	newRedeemHandler(t, svcMock)(rr, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not-json")))

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
