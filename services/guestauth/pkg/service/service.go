package service

import (
	"context"
	"sync/atomic"

	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/config"
)

// GuestauthService is the service responsible for creating and validating guest login tokens.
type GuestauthService struct {
	log     log.Logger
	cfg     *config.Config
	ctx     context.Context
	stopCh  chan struct{}
	stopped atomic.Bool
}

// New returns a guestauth service
func New(opts ...Option) (*GuestauthService, error) {
	o := &Options{}
	for _, opt := range opts {
		opt(o)
	}

	s := &GuestauthService{
		log:    o.Logger,
		cfg:    o.Config,
		ctx:    o.Context,
		stopCh: make(chan struct{}, 1),
	}

	return s, nil
}

// Run runs the service
func (s *GuestauthService) Run() error {
	s.log.Info().Msg("starting guestauth service")

	<-s.stopCh

	s.log.Info().Msg("guestauth service stopped")
	return nil
}

// Close shuts down the service
func (s *GuestauthService) Close() {
	if s.stopped.CompareAndSwap(false, true) {
		close(s.stopCh)
	}
}
