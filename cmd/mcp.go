package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/mcp"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// newMCPCmd wires `octo-cli mcp serve`: octo-cli as an MCP server exposing the
// three meta-tools (search_ops / describe_op / call_op) over stdio (default) or
// HTTP (JSON-RPC over POST). It is credential-free to start (delayed validation, design
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
		facade           string
	)
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Serve the MCP protocol over stdio (default) or HTTP (JSON-RPC over POST)",
		Long: `Serve octo-cli as an MCP server.

  octo-cli mcp serve                     # stdio (local / trusted-client transport)
  octo-cli mcp serve --http :8080        # HTTP: JSON-RPC over POST (production transport)

By default three meta-tools are exposed: search_ops, describe_op, call_op. The
--facade flag selects the tool surface: "three" (default), "two" (the
Skill-driven get_skill + execute facade), or "both" (all five, for controlled
side-by-side comparison). Either way the full operation set is discovered
dynamically rather than expanded into hundreds of resident MCP tools, so a
client's tools/list stays small.

The HTTP transport is JSON-RPC request/response over POST (one request, one
response); it does not yet serve an SSE stream or a server-managed session.
Origin validation guards it against DNS-rebinding: set OCTO_MCP_ALLOWED_ORIGINS
(comma-separated) to allow browser origins; unset, only loopback origins are
accepted and non-browser clients (no Origin header) are allowed.

Over-privilege防护: for stdio the forced space / channel / on-behalf-of values
come from --space / --force-* flags (or OCTO_SPACE_ID / OCTO_FORCE_* env); for
HTTP they come per-connection from request headers (X-Space-Id,
X-Octo-Channel-Id, X-Octo-Channel-Type, X-Octo-On-Behalf-Of) and the bearer
Authorization header, so one connection can never act in another's scope.

Local-file uploads (multipart file_path): over stdio the transport is trusted
and a file_path is passed through. Over HTTP an untrusted client may only read
files under an operator-configured root: set OCTO_MCP_UPLOAD_ROOT to a confined
directory to enable uploads (paths are cleaned and symlink-resolved and must
stay within it); unset, HTTP local uploads are refused. Deployment boundary:
run stdio only for a local/trusted client, and run HTTP behind a loopback bind
or a TLS-terminating reverse proxy that sets the trusted headers.`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"skipValidation": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			fac, ok := mcp.ParseFacade(facade)
			if !ok {
				return output.ErrValidation(
					fmt.Sprintf("invalid --facade %q", facade),
					"choose one of: three (default), two, both")
			}
			tc := mcp.TrustedContext{
				SpaceID:     firstNonEmpty(f.Globals.Space, os.Getenv("OCTO_SPACE_ID")),
				ChannelID:   firstNonEmpty(forceChannelID, os.Getenv("OCTO_FORCE_CHANNEL_ID")),
				ChannelType: firstNonEmpty(forceChannelType, os.Getenv("OCTO_FORCE_CHANNEL_TYPE")),
				OnBehalfOf:  firstNonEmpty(forceOnBehalfOf, os.Getenv("OCTO_FORCE_ON_BEHALF_OF")),
			}
			build := func(ff *cmdutil.Factory) *cobra.Command { return NewRootCmd(ff) }

			if httpAddr != "" {
				return serveHTTP(cmd.Context(), httpAddr, build, tc, facade)
			}
			srv, err := mcp.NewServer(build)
			if err != nil {
				return err
			}
			srv.WithTrustedContext(tc)
			srv.WithFacade(fac)
			return srv.ServeStdio(cmd.Context(), f.IOStreams.In, f.IOStreams.Out)
		},
	}
	serve.Flags().StringVar(&httpAddr, "http", "", "serve HTTP (JSON-RPC over POST) on this address (e.g. :8080); default transport is stdio")
	serve.Flags().StringVar(&forceChannelID, "force-channel-id", "", "stdio: force this channel_id on session-bound message ops")
	serve.Flags().StringVar(&forceChannelType, "force-channel-type", "", "stdio: force this channel_type on session-bound message ops")
	serve.Flags().StringVar(&forceOnBehalfOf, "force-on-behalf-of", "", "stdio: force this on_behalf_of identity")
	serve.Flags().StringVar(&facade, "facade", "three", "tool surface to expose: three (default), two (get_skill + execute), or both")
	return serve
}

// serveHTTP runs the HTTP (JSON-RPC over POST) server until the context is cancelled or a
// termination signal arrives, then shuts down gracefully so no request is
// dropped mid-flight. facade is the already-validated selector string; it is
// re-parsed here so the cmd package need not name the mcp package's internal
// facade type.
func serveHTTP(ctx context.Context, addr string, build mcp.RootBuilder, tc mcp.TrustedContext, facade string) error {
	fac, _ := mcp.ParseFacade(facade)
	h, err := mcp.NewHTTPHandler(build, tc, fac)
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
