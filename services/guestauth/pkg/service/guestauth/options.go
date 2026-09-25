package guestauth

import (
	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/config"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/jwt"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/todo/pool"
)

type Option func(*Options)

// Options for the guestauth service
type Options struct {
	GatewaySelector pool.Selectable[gateway.GatewayAPIClient]
	ServiceAccount  config.ServiceAccount
	JWT             *jwt.JwtService
}

// GatewaySelector adds a grpc client selector for the gateway service
func GatewaySelector(gatewaySelector pool.Selectable[gateway.GatewayAPIClient]) Option {
	return func(o *Options) {
		o.GatewaySelector = gatewaySelector
	}
}

// ServiceAccount configures a service account for the guestauth service
func ServiceAccount(sa config.ServiceAccount) Option {
	return func(o *Options) {
		o.ServiceAccount = sa
	}
}

// JWT configures the jwt service for the guestauth service
func JWT(m *jwt.JwtService) Option {
	return func(o *Options) {
		o.JWT = m
	}
}
