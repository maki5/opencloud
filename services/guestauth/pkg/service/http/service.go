package http

import (
	"errors"
	"time"

	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
)

var ErrExpired = errors.New("token expired")
var ErrAlreadyRedeemed = errors.New("token already redeemed")

func NewService(tokenSvc *token.TokenService, store storage.Storage, opts ...Option) (*svc, error) {
	o := newOptions(opts...)

	return &svc{
		log:      o.Logger,
		tokenSvc: tokenSvc,
		store:    store,
	}, nil
}

// svc provides the logic behind the http endpoints of the guestauth service.
type svc struct {
	log      log.Logger
	tokenSvc *token.TokenService
	store    storage.Storage
}

func (s *svc) VerifyToken(tokenString string) (storage.Record, error) {
	shareIDHash, err := s.tokenSvc.ShareIDHash(tokenString)
	if err != nil {
		return storage.Record{}, err
	}

	rec, err := s.store.Get(shareIDHash)
	if err != nil {
		return storage.Record{}, err
	}

	if err := s.tokenSvc.Verify(tokenString, rec.SecretHash); err != nil {
		return storage.Record{}, err
	}

	if !rec.Expiry.IsZero() && rec.Expiry.Before(time.Now()) {
		return storage.Record{}, ErrExpired
	}

	if rec.Redeemed {
		return storage.Record{}, ErrAlreadyRedeemed
	}

	return rec, nil
}
