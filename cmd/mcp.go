package cmd

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/mcp"
)

// newMCPCmd wires `octo-cli mcp serve`: octo-cli as an MCP server exposing the
// three meta-tools (search_ops / describe_op / call_op) over stdio (default) or
// streamable HTTP. It is credential-free to start (delayed validation, design
// §9 item 6) — call_op resolves the credential lazily — so the annotation opts
// the subtree out of the root auth gate while each call still authenticates.
func newMCPCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "mcp",
		Short:       "Run octo-cli as an MCP (Model Context Protocol) server",
		Annotations: map[string]string{"skipValidation": "true"},
		RunE:        rejectMCPBareParent,
	}
	cmd.AddCommand(newMCPServeCmd(f))
	return cmd
}

func rejectMCPBareParent(cmd *cobra.Command, _ []string) error { return cmd.Help() }

func newMCPServeCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		httpAddr         string
		forceChannelID   string
		forceChannelType string
		forceOnBehalfOf  string
	)
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Serve the MCP protocol over stdio (default) or streamable HTTP",
		Long: `Serve octo-cli as an MCP server.

  octo-cli mcp serve                     # stdio (local / trusted-client transport)
  octo-cli mcp serve --http :8080        # streamable HTTP (production transport)

Three meta-tools are exposed regardless of transport: search_ops, describe_op,
call_op. The full operation set is discovered dynamically rather than expanded
into hundreds of resident MCP tools, so a client's tools/list stays small.

Over-privilege防护: for stdio the forced space / channel / on-behalf-of values
come from --space / --force-* flags (or OCTO_SPACE_ID / OCTO_FORCE_* env); for
HTTP they come per-connection from request headers (X-Space-Id,
X-Octo-Channel-Id, X-Octo-Channel-Type, X-Octo-On-Behalf-Of) and the bearer
Authorization header, so one connection can never act in another's scope.`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"skipValidation": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			tc := mcp.TrustedContext{
				SpaceID:     firstNonEmpty(f.Globals.Space, os.Getenv("OCTO_SPACE_ID")),
				ChannelID:   firstNonEmpty(forceChannelID, os.Getenv("OCTO_FORCE_CHANNEL_ID")),
				ChannelType: firstNonEmpty(forceChannelType, os.Getenv("OCTO_FORCE_CHANNEL_TYPE")),
				OnBehalfOf:  firstNonEmpty(forceOnBehalfOf, os.Getenv("OCTO_FORCE_ON_BEHALF_OF")),
			}
			build := func(ff *cmdutil.Factory) *cobra.Command { return NewRootCmd(ff) }

			if httpAddr != "" {
				return serveHTTP(cmd.Context(), httpAddr, build, tc)
			}
			srv, err := mcp.NewServer(build)
			if err != nil {
				return err
			}
			srv.WithTrustedContext(tc)
			return srv.ServeStdio(cmd.Context(), f.IOStreams.In, f.IOStreams.Out)
		},
	}
	serve.Flags().StringVar(&httpAddr, "http", "", "serve streamable HTTP on this address (e.g. :8080); default transport is stdio")
	serve.Flags().StringVar(&forceChannelID, "force-channel-id", "", "stdio: force this channel_id on session-bound message ops")
	serve.Flags().StringVar(&forceChannelType, "force-channel-type", "", "stdio: force this channel_type on session-bound message ops")
	serve.Flags().StringVar(&forceOnBehalfOf, "force-on-behalf-of", "", "stdio: force this on_behalf_of identity")
	return serve
}

// serveHTTP runs the streamable-HTTP server until the context is cancelled or a
// termination signal arrives, then shuts down gracefully so no request is
// dropped mid-flight.
func serveHTTP(ctx context.Context, addr string, build mcp.RootBuilder, tc mcp.TrustedContext) error {
	h, err := mcp.NewHTTPHandler(build, tc)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-sigCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
