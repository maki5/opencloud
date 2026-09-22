package events

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/reva/v2/pkg/events"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var tracer trace.Tracer

func init() {
	tracer = otel.Tracer("github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/events")
}

var (
	_numConsumersDefault = 1
)

// GuestauthService consumes events for the guestauth service
type GuestauthService struct {
	ctx    context.Context
	log    log.Logger
	stream events.Stream

	numConsumers int

	events []events.Unmarshaller

	stopCh  chan struct{}
	stopped *atomic.Bool
}

// New creates a new GuestauthService
func New(stream events.Stream, opts ...Option) (*GuestauthService, error) {
	o := &Options{
		NumConsumers: _numConsumersDefault,
	}
	for _, opt := range opts {
		opt(o)
	}

	s := &GuestauthService{
		ctx:          o.Context,
		log:          o.Logger,
		stream:       stream,
		events:       o.RegisteredEvents,
		numConsumers: o.NumConsumers,
		stopCh:       make(chan struct{}, 1),
		stopped:      new(atomic.Bool),
	}

	return s, nil
}

// Run to fulfil Runner interface
func (s *GuestauthService) Run() error {
	ch, err := events.Consume(s.stream, "guestauth", s.events...)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	s.log.Debug().Int("worker.count", s.numConsumers).
		Str("messaging.consumer.group.name", "guestauth").
		Str("messaging.system", "nats").
		Str("messaging.operation.name", "receive").
		Msg("starting event processing workers")

	// start workers
	for i := range s.numConsumers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case e, ok := <-ch:
					if !ok {
						return
					}
					if err := s.processEvent(e); err != nil {
						s.log.Error().Err(err).
							Int("worker", workerID).
							Interface("event", e).
							Msg("failed to process event")
					}
				}
			}
		}(i)
	}

	// wait for stop signal
	<-s.stopCh
	cancel() // signal workers to stop
	wg.Wait()

	return nil
}

// Close will make the service to stop processing, so the `Run`
// method can finish.
func (s *GuestauthService) Close() {
	if s.stopped.CompareAndSwap(false, true) {
		close(s.stopCh)
	}
}

// processEvent dispatches an event to the matching handler.
func (s *GuestauthService) processEvent(e events.Event) error {
	ctx := e.GetTraceContext(s.ctx)
	ctx, span := tracer.Start(ctx, "processEvent")
	defer span.End()

	s.log.Debug().Interface("event", e).Msg("processing event")

	switch ev := e.Event.(type) {
	case events.ShareCreated:
		return s.handleShareCreated(ctx, ev)
	case events.ShareRemoved:
		return s.handleShareRemoved(ctx, ev)
	case events.ShareExpired:
		return s.handleShareExpired(ctx, ev)
	default:
		s.log.Warn().
			Str("eventtype", e.Type).
			Msg("unhandled event")
	}

	return nil
}

// handleShareCreated handles a share created event.
func (s *GuestauthService) handleShareCreated(ctx context.Context, ev events.ShareCreated) error {
	_, span := tracer.Start(ctx, "handleShareCreated")
	defer span.End()

	s.log.Debug().Interface("event", ev).Msg("share created event received")

	return nil
}

// handleShareRemoved handles a share removed event.
func (s *GuestauthService) handleShareRemoved(ctx context.Context, ev events.ShareRemoved) error {
	_, span := tracer.Start(ctx, "handleShareRemoved")
	defer span.End()

	s.log.Debug().Interface("event", ev).Msg("share removed event received")

	return nil
}

// handleShareExpired handles a share expired event.
func (s *GuestauthService) handleShareExpired(ctx context.Context, ev events.ShareExpired) error {
	_, span := tracer.Start(ctx, "handleShareExpired")
	defer span.End()

	s.log.Debug().Interface("event", ev).Msg("share expired event received")

	return nil
}
