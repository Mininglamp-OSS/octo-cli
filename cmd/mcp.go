package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
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
		allowLocalUpload bool
	)
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Serve the MCP protocol over stdio (default) or HTTP (JSON-RPC over POST)",
		Long: `Serve octo-cli as an MCP server.

  octo-cli mcp serve                     # stdio (local / trusted-client transport)
  octo-cli mcp serve --http :8080        # HTTP: JSON-RPC over POST (production transport)

Three meta-tools are exposed regardless of transport: search_ops, describe_op,
call_op. The full operation set is discovered dynamically rather than expanded
into hundreds of resident MCP tools, so a client's tools/list stays small.

The HTTP transport is JSON-RPC request/response over POST (one request, one
response); it does not yet serve an SSE stream or a server-managed session.

Deployment (HTTP):
  - No TLS is provided. --http binds every interface unless you give a loopback
    host (e.g. --http 127.0.0.1:8080). Terminate TLS at a reverse proxy and/or
    bind loopback; the bearer is a long-lived bot token sent per request.
  - Trusted-context headers (X-Space-Id, X-Octo-Channel-Id, X-Octo-Channel-Type,
    X-Octo-On-Behalf-Of) are a gateway convenience, NOT authentication: operator
    --force-* / --space values always win, and a header may only fill a field
    the operator did not force. Only a proxy that sets and strips these headers
    makes them trustworthy.
  - OCTO_MCP_ALLOWED_ORIGINS (comma-separated) permits browser origins; unset,
    only loopback origins are accepted (anti DNS-rebinding). Setting it replaces
    the loopback default, so list your loopback origin too if you still use it.
  - initialize / tools/list / search_ops / describe_op / resources/read need no
    bearer, so any peer that can reach the port can enumerate the catalog.

Multipart file_path uploads (file.upload, html.asset.add, loop.attachment.upload)
are default-deny on BOTH transports so a model cannot read an arbitrary local
file: set OCTO_MCP_UPLOAD_ROOT to confine uploads to one directory (path is
cleaned and symlink-resolved and must stay within it), or, on stdio only, pass
--allow-local-upload to allow unconfined uploads on a trusted-local host. HTTP
never allows unconfined uploads.

Over-privilege protection: for stdio the forced space / channel / on-behalf-of
values come from --space / --force-* flags (or OCTO_SPACE_ID / OCTO_FORCE_*
env); for HTTP they come per-connection from the operator config with request
headers filling only unforced fields, plus the bearer Authorization header, so
one connection can never act in another's scope.

--dry-run turns the whole server into a rehearsal: every call_op prints the
request it would send (method, url, masked headers, body) as a "dry_run": true
envelope and performs no backend mutation for the server's lifetime.`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"skipValidation": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Validate the operator's --timeout once at startup so a malformed
			// value fails loudly here rather than emitting a diagnostic into
			// every call_op's stderr.
			if t := f.Globals.Timeout; t != "" {
				if _, err := time.ParseDuration(t); err != nil {
					return fmt.Errorf("invalid --timeout %q: %w", t, err)
				}
			}
			tc := mcp.TrustedContext{
				SpaceID:     firstNonEmpty(f.Globals.Space, os.Getenv("OCTO_SPACE_ID")),
				ChannelID:   firstNonEmpty(forceChannelID, os.Getenv("OCTO_FORCE_CHANNEL_ID")),
				ChannelType: firstNonEmpty(forceChannelType, os.Getenv("OCTO_FORCE_CHANNEL_TYPE")),
				OnBehalfOf:  firstNonEmpty(forceOnBehalfOf, os.Getenv("OCTO_FORCE_ON_BEHALF_OF")),
			}
			// Carry the operator's credential-selector, limit, and dry-run globals
			// into each call so --profile / --bot-id / --timeout / --no-retry /
			// --dry-run are honored rather than silently dropped by the per-call
			// factory. --dry-run makes every served call a rehearsal.
			base := cmdutil.GlobalOptions{
				BotID:   f.Globals.BotID,
				Profile: f.Globals.Profile,
				Timeout: f.Globals.Timeout,
				NoRetry: f.Globals.NoRetry,
				DryRun:  f.Globals.DryRun,
			}
			build := func(ff *cmdutil.Factory) *cobra.Command { return NewRootCmd(ff) }

			if httpAddr != "" {
				return serveHTTP(cmd.Context(), httpAddr, build, tc, base, f.IOStreams.ErrOut)
			}
			srv, err := mcp.NewServer(build)
			if err != nil {
				return err
			}
			srv.WithTrustedContext(tc).WithBaseGlobals(base).
				WithUploadPolicy(os.Getenv("OCTO_MCP_UPLOAD_ROOT"), allowLocalUpload)
			return srv.ServeStdio(cmd.Context(), f.IOStreams.In, f.IOStreams.Out)
		},
	}
	serve.Flags().StringVar(&httpAddr, "http", "", "serve HTTP (JSON-RPC over POST) on this address (e.g. :8080); default transport is stdio")
	serve.Flags().StringVar(&forceChannelID, "force-channel-id", "", "force this channel_id on session-bound message ops (stdio + HTTP)")
	serve.Flags().StringVar(&forceChannelType, "force-channel-type", "", "force this channel_type on session-bound message ops (stdio + HTTP)")
	serve.Flags().StringVar(&forceOnBehalfOf, "force-on-behalf-of", "", "force this on_behalf_of identity (stdio + HTTP)")
	serve.Flags().BoolVar(&allowLocalUpload, "allow-local-upload", false, "stdio only: allow unconfined multipart file_path uploads (trusted-local hosts); default-deny unless OCTO_MCP_UPLOAD_ROOT is set")
	return serve
}

// serveHTTP runs the HTTP (JSON-RPC over POST) server until the context is cancelled or a
// termination signal arrives, then shuts down gracefully so no request is
// dropped mid-flight. Read/Idle timeouts bound a slow or idle client;
// WriteTimeout is left unset because a legitimate --page-all response can be
// long, and per-call bounding is the operator's --timeout instead.
func serveHTTP(ctx context.Context, addr string, build mcp.RootBuilder, tc mcp.TrustedContext, base cmdutil.GlobalOptions, warn io.Writer) error {
	h, err := mcp.NewHTTPHandler(build, tc, base)
	if err != nil {
		return err
	}
	if !isLoopbackAddr(addr) {
		fmt.Fprintf(warn, "warning: MCP HTTP server binding %q is not loopback and serves cleartext; terminate TLS at a proxy and/or bind 127.0.0.1\n", addr)
	}
	srv := newMCPHTTPServer(addr, h)

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

// newMCPHTTPServer builds the MCP HTTP server with the connection timeouts that
// bound a slow or idle client. WriteTimeout is intentionally left unset because
// a legitimate --page-all response can be long; per-call bounding is the
// operator's --timeout instead.
func newMCPHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// isLoopbackAddr reports whether a listen address binds only the loopback
// interface. A bare ":port" or a non-loopback host is treated as public.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
