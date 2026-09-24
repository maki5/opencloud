package guestauth

import (
	"errors"
	"time"

	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
)

var ErrExpired = errors.New("token expired")
var ErrAlreadyRedeemed = errors.New("token already redeemed")

// GuestAuthService contains the business logic shared by guestauth transport services.
type GuestAuthService struct {
	tokenSvc *token.TokenService
	store    storage.Storage
}

func NewGuestAuthService(tokenSvc *token.TokenService, store storage.Storage) *GuestAuthService {
	return &GuestAuthService{
		tokenSvc: tokenSvc,
		store:    store,
	}
}

func (s *GuestAuthService) CreateToken(shareID string) (*token.Token, error) {
	tok, err := s.tokenSvc.Generate(shareID)
	if err != nil {
		return nil, err
	}

	// ShareCreated carries no expiration; expiry is checked against the share when the token is redeemed.
	if err := s.store.Add(storage.Record{
		ShareID:     shareID,
		ShareIDHash: tok.ShareIDHash,
		SecretHash:  tok.SecretHash,
		Redeemed:    false,
	}); err != nil {
		return nil, err
	}

	return tok, nil
}

// VerifyToken validates a token and returns its stored record.
func (s *GuestAuthService) VerifyToken(tokenString string) (storage.Record, error) {
	tok, err := s.tokenSvc.Parse(tokenString)
	if err != nil {
		return storage.Record{}, err
	}

	rec, err := s.store.Get(tok.ShareIDHash)
	if err != nil {
		return storage.Record{}, err
	}

	if err := s.tokenSvc.Verify(*tok, rec.SecretHash); err != nil {
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

// CleanupShare removes a share's token record from storage. Missing records are ignored.
func (s *GuestAuthService) CleanupShare(shareID string) error {
	shareIDHash := token.Hash(shareID)
	err := s.store.Remove(shareIDHash)
	if err != nil && err != storage.ErrNotFound {
		return err
	}

	return nil
}
