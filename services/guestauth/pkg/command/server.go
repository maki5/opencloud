package command

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/opencloud-eu/opencloud/pkg/config/configlog"
	"github.com/opencloud-eu/opencloud/pkg/generators"
	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/pkg/runner"
	"github.com/opencloud-eu/opencloud/pkg/version"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/config"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/config/parser"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/metrics"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/server/debug"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/server/http"
	svcEvents "github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/events"
	svcHttp "github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/http"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/storage"
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/service/token"
	"github.com/opencloud-eu/reva/v2/pkg/events"
	"github.com/opencloud-eu/reva/v2/pkg/events/stream"
)

var _registeredEvents = []events.Unmarshaller{
	events.ShareCreated{},
	events.ShareRemoved{},
	events.ShareExpired{},
}

// Server is the entrypoint for the server command.
func Server(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: fmt.Sprintf("start the %s service without runtime (unsupervised mode)", cfg.Service.Name),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return configlog.ReturnFatal(parser.ParseConfig(cfg))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.Configure(cfg.Service.Name, cfg.Commons, cfg.LogLevel)

			gr := runner.NewGroup()
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			mtrcs := metrics.New()
			mtrcs.BuildInfo.WithLabelValues(version.GetString()).Set(1)

			tokenSvc := token.NewTokenService()
			store := storage.NewFileStorage(cfg.Storage.RootDirectory)
			redeemSvc, err := svcHttp.NewService(tokenSvc, store)
			if err != nil {
				logger.Error().Err(err).Msg("Failed to initialize http service")
				return err
			}

			if !cfg.HTTP.Disabled {
				server, err := http.Server(
					http.Logger(logger),
					http.Context(ctx),
					http.Config(cfg),
					http.Service(redeemSvc),
				)
				if err != nil {
					logger.Info().
						Err(err).
						Str("transport", "http").
						Msg("Failed to initialize server")

					return err
				}

				gr.Add(runner.NewGoMicroHttpServerRunner(cfg.Service.Name+".http", server))
			} else {
				logger.Info().Msg("HTTP server disabled, not starting HTTP service")
			}

			if !cfg.Events.Disabled {
				connName := generators.GenerateConnectionName(cfg.Service.Name, generators.NTypeBus)
				evStream, err := stream.NatsFromConfig(connName, false, stream.NatsConfig{
					Endpoint:             cfg.Events.Endpoint,
					Cluster:              cfg.Events.Cluster,
					EnableTLS:            cfg.Events.EnableTLS,
					TLSInsecure:          cfg.Events.TLSInsecure,
					TLSRootCACertificate: cfg.Events.TLSRootCACertificate,
					AuthUsername:         cfg.Events.AuthUsername,
					AuthPassword:         cfg.Events.AuthPassword,
				})
				if err != nil {
					logger.Error().Err(err).Msg("Failed to initialize event stream")
					return err
				}

				guestAuth, err := svcEvents.New(
					evStream,
					svcEvents.Logger(logger),
					svcEvents.Context(ctx),
					svcEvents.RegisteredEvents(_registeredEvents),
					svcEvents.NumConsumers(cfg.NumConsumers),
				)
				if err != nil {
					logger.Error().Err(err).Str("transport", "event").Msg("Failed to initialize server")
					return err
				}

				gr.Add(runner.New(cfg.Service.Name+".svc", func() error {
					return guestAuth.Run()
				}, func() {
					guestAuth.Close()
				}))
			} else {
				logger.Info().Msg("event listening disabled, not starting event service")
			}

			{
				debugServer, err := debug.Server(
					debug.Logger(logger),
					debug.Context(ctx),
					debug.Config(cfg),
				)
				if err != nil {
					logger.Info().Err(err).Str("server", "debug").Msg("Failed to initialize server")
					return err
				}

				gr.Add(runner.NewGolangHttpServerRunner(cfg.Service.Name+".debug", debugServer))
			}

			grResults := gr.Run(ctx)

			// return the first non-nil error found in the results
			for _, grResult := range grResults {
				if grResult.RunnerError != nil {
					return grResult.RunnerError
				}
			}
			return nil
		},
	}
}
