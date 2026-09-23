package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/opencloud-eu/opencloud/pkg/log"
	svchttp "github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/http"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	token "github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
)

type RedeemService interface {
	VerifyToken(tokenString string) (storage.Record, error)
}

type RedeemRequest struct {
	Token string `json:"token"`
}

func RedeemHandler(log log.Logger, s RedeemService) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RedeemRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Debug().Err(err).Msg("request body is malformed")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		_, err := s.VerifyToken(req.Token)
		if err != nil {
			switch {
			case errors.Is(err, svchttp.ErrExpired) || errors.Is(err, svchttp.ErrAlreadyRedeemed):
				log.Debug().Err(err).Msg("token expired or already redeemed")
				w.WriteHeader(http.StatusGone)
			case errors.Is(err, storage.ErrNotFound):
				log.Debug().Err(err).Msg("no token record found")
				w.WriteHeader(http.StatusNotFound)
			case errors.Is(err, token.ErrInvalidToken):
				log.Debug().Err(err).Msg("token is invalid")
				w.WriteHeader(http.StatusUnauthorized)
			default:
				log.Error().Err(err).Msg("error verifying token")
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}

		// session create should be here
		w.WriteHeader(http.StatusOK)
	}
}
