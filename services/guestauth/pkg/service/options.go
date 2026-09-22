package service

import (
	"context"

	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/config"
)

// Option for the guestauth service
type Option func(*Options)

// Options for the guestauth service
type Options struct {
	Logger  log.Logger
	Config  *config.Config
	Context context.Context
}

// Logger configures a logger for the guestauth service
func Logger(log log.Logger) Option {
	return func(o *Options) {
		o.Logger = log
	}
}

// Config adds the config for the guestauth service
func Config(c *config.Config) Option {
	return func(o *Options) {
		o.Config = c
	}
}

// Context adds a context for the guestauth service
func Context(ctx context.Context) Option {
	return func(o *Options) {
		o.Context = ctx
	}
}
