package guestauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	collaboration "github.com/cs3org/go-cs3apis/cs3/sharing/collaboration/v1beta1"

	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/config"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/jwt"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/todo/pool"
	"github.com/opencloud-eu/reva/v2/pkg/utils"
)

var ErrExpired = errors.New("token expired")
var ErrAlreadyRedeemed = errors.New("token already redeemed")
var ErrShareNotFound = errors.New("share not found")
var ErrShareExpired = errors.New("share expired")

// GuestAuthService contains the business logic shared by guestauth transport services.
type GuestAuthService struct {
	tokenSvc        *token.TokenService
	store           storage.Storage
	gatewaySelector pool.Selectable[gateway.GatewayAPIClient]
	serviceAccount  config.ServiceAccount
	jwtService      *jwt.JwtService
}

func NewGuestAuthService(tokenSvc *token.TokenService, store storage.Storage, opts ...Option) *GuestAuthService {
	o := &Options{}
	for _, opt := range opts {
		opt(o)
	}

	return &GuestAuthService{
		tokenSvc:        tokenSvc,
		store:           store,
		gatewaySelector: o.GatewaySelector,
		serviceAccount:  o.ServiceAccount,
		jwtService:      o.JWT,
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

// Redeem validates a token and its share and exchanges them for a session token.
func (s *GuestAuthService) Redeem(ctx context.Context, tokenString string) (string, error) {
	rec, err := s.verifyToken(tokenString)
	if err != nil {
		return "", err
	}

	if _, err := s.validateShare(ctx, rec.ShareID); err != nil {
		return "", err
	}

	sessionToken, err := s.jwtService.Sign(rec.ShareID)
	if err != nil {
		return "", err
	}

	if err := s.store.Redeem(rec.ShareIDHash); err != nil {
		return "", err
	}

	return sessionToken, nil
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

// VerifyToken validates a token and returns its stored record.
func (s *GuestAuthService) verifyToken(tokenString string) (storage.Record, error) {
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

// validateShare extracts the share information from the gateway and checks its existence and expiration.
func (s *GuestAuthService) validateShare(ctx context.Context, shareID string) (*collaboration.Share, error) {
	gwc, err := s.gatewaySelector.Next()
	if err != nil {
		return nil, err
	}

	ctx, err = utils.GetServiceUserContextWithContext(ctx, gwc, s.serviceAccount.ServiceAccountID, s.serviceAccount.ServiceAccountSecret)
	if err != nil {
		return nil, err
	}

	resp, err := gwc.GetShare(ctx, &collaboration.GetShareRequest{
		Ref: &collaboration.ShareReference{
			Spec: &collaboration.ShareReference_Id{
				Id: &collaboration.ShareId{
					OpaqueId: shareID,
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	switch resp.GetStatus().GetCode() {
	case rpc.Code_CODE_OK:
	case rpc.Code_CODE_NOT_FOUND:
		return nil, ErrShareNotFound
	default:
		return nil, fmt.Errorf("could not get share %s: %s", shareID, resp.GetStatus().GetMessage())
	}

	share := resp.GetShare()
	if share == nil {
		return nil, ErrShareNotFound
	}

	if exp := utils.TSToTime(share.GetExpiration()); !exp.IsZero() && exp.Before(time.Now()) {
		return nil, ErrShareExpired
	}

	return share, nil
}
