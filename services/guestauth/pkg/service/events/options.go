package events

import (
	"context"

	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/guestauth"
	"github.com/opencloud-eu/reva/v2/pkg/events"
)

// Option for the guestauth service
type Option func(*Options)

// Options for the guestauth service
type Options struct {
	Context          context.Context
	Logger           log.Logger
	Stream           events.Stream
	RegisteredEvents []events.Unmarshaller
	NumConsumers     int
	GuestAuthService *guestauth.GuestAuthService
}

// Context configures a context for the guestauth service
func Context(ctx context.Context) Option {
	return func(o *Options) {
		o.Context = ctx
	}
}

// Logger configures a logger for the guestauth service
func Logger(log log.Logger) Option {
	return func(o *Options) {
		o.Logger = log
	}
}

// Stream configures an event stream for the guestauth service
func Stream(s events.Stream) Option {
	return func(o *Options) {
		o.Stream = s
	}
}

// RegisteredEvents registers the events the service should listen to
func RegisteredEvents(e []events.Unmarshaller) Option {
	return func(o *Options) {
		o.RegisteredEvents = e
	}
}

// NumConsumers configures the amount of concurrent event consumers
func NumConsumers(num int) Option {
	return func(o *Options) {
		o.NumConsumers = num
	}
}

// GuestAuthService configures the guest auth domain service.
func GuestAuthService(s *guestauth.GuestAuthService) Option {
	return func(o *Options) {
		o.GuestAuthService = s
	}
}
