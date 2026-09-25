package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/config"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/guestauth"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	token "github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
)

// RedeemRequest is the request body for token redemption.
type RedeemRequest struct {
	Token string `json:"token"`
}

// RedeemHandler validates the token submitted to the redeem endpoint.
func RedeemHandler(log log.Logger, s *guestauth.GuestAuthService, cfg *config.Config) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RedeemRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Debug().Err(err).Msg("request body is malformed")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		sessionToken, err := s.Redeem(r.Context(), req.Token)
		if err != nil {
			switch {
			case errors.Is(err, guestauth.ErrAlreadyRedeemed):
				log.Debug().Err(err).Msg("token already redeemed")
				w.WriteHeader(http.StatusConflict)
			case errors.Is(err, guestauth.ErrExpired):
				log.Debug().Err(err).Msg("token expired")
				w.WriteHeader(http.StatusGone)
			case errors.Is(err, storage.ErrNotFound):
				log.Debug().Err(err).Msg("token not found")
				w.WriteHeader(http.StatusNotFound)
			case errors.Is(err, token.ErrInvalidToken):
				log.Debug().Err(err).Msg("token is invalid")
				w.WriteHeader(http.StatusUnauthorized)
			case errors.Is(err, guestauth.ErrShareNotFound):
				log.Debug().Err(err).Msg("share not found")
				w.WriteHeader(http.StatusNotFound)
			case errors.Is(err, guestauth.ErrShareExpired):
				log.Debug().Err(err).Msg("share expired")
				w.WriteHeader(http.StatusGone)

			default:
				log.Error().Err(err).Msg("error redeeming token")
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     cfg.JWT.CookieName,
			Value:    sessionToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   cfg.JWT.CookieSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(cfg.JWT.TTL.Seconds()),
		})
		w.WriteHeader(http.StatusOK)
	}
}
