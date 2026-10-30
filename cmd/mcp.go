package cmd

import (
	"context"
	"errors"
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

func newMCPServeCmd(f *cmdutil.Factory) *cobra.Command { //nolint:gocyclo // operator startup validation is ordered before any service resources or listener
	var (
		httpAddr         string
		forceChannelID   string
		forceChannelType string
		forceOnBehalfOf  string
		allowLocalUpload bool
		facade           string
		policyDebug      bool
		policyDebugFile  string
	)
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Serve the MCP protocol over stdio (default) or HTTP (JSON-RPC over POST)",
		Long: `Serve octo-cli as an MCP server.

  octo-cli mcp serve                     # stdio (local / trusted-client transport)
  octo-cli mcp serve --http :8080        # HTTP: JSON-RPC over POST (production transport)

The existing facade meta-tools are search_ops, describe_op, call_op by default. The
--facade flag selects the tool surface: "three" (default), "two" (the
Skill-driven get_skill + execute facade), or "both" (the five existing meta-tools, for controlled
side-by-side comparison). Either way the full operation set is discovered
dynamically rather than expanded into hundreds of resident MCP tools, so a
client's tools/list stays small. Each facade additionally exposes one dedicated
get_secret_download_url tool (4 / 3 / 6 tools total). It requires private shared
Redis and an explicit client-reachable public HTTPS origin; see the shipped
octo-shared/secret-download.md. Stdio publishes to the same Redis as that
HTTP MCP download service, without a local listener. Missing/invalid feature
configuration fails closed for this tool; existing tools remain available.

The HTTP transport is JSON-RPC request/response over POST (one request, one
response); it does not yet serve an SSE stream or a server-managed session.

Deployment (HTTP):
  - No TLS is provided. --http binds every interface unless you give a loopback
    host (e.g. --http 127.0.0.1:8080). Terminate TLS at a reverse proxy and/or
    bind loopback; the bearer is a long-lived bot token sent per request.
  - Trusted context comes only from operator --force-* / --space values or
    OCTO_FORCE_* / OCTO_SPACE_ID. Requests containing X-Space-Id,
    X-Octo-Channel-Id, X-Octo-Channel-Type or X-Octo-On-Behalf-Of are refused
    with HTTP 403, even when empty or matching operator values. There is no
    trusted-proxy header mode; configure a fixed server context instead.
  - OCTO_MCP_ALLOWED_ORIGINS (comma-separated) permits browser origins; unset,
    only loopback origins are accepted (anti DNS-rebinding). Setting it replaces
    the loopback default, so list your loopback origin too if you still use it.
    This is an origin gate, not browser CORS support; OPTIONS preflight is not
    implemented. Use a non-browser client or a proxy that implements CORS.
  - Every HTTP protocol request requires a bf_ User Bot bearer. Every tool
    call, including discovery and direct secret access, verifies Bot identity
    and the current per-Bot policy before proceeding.

Multipart file_path uploads (file.upload, html.asset.add, attachment.upload)
are default-deny on BOTH transports so a model cannot read an arbitrary local
file: set OCTO_MCP_UPLOAD_ROOT to confine uploads to one directory (path is
opened relative to directory handles, rejecting symlinks and non-regular files),
or, on stdio only, pass
--allow-local-upload to allow unconfined uploads on a trusted-local host. HTTP
never allows unconfined uploads.

Over-privilege protection: for stdio the forced space / channel
values come from --space / --force-* flags (or OCTO_SPACE_ID / OCTO_FORCE_*
env); HTTP uses the same operator config and a separate Authorization bearer
per request. Unconfigured business target arguments remain caller-supplied
inputs subject to backend authorization; they do not become trusted context.

Strict policy requires params._meta.octo on every tools/call. This is a client
source declaration, never an execution identity or a business destination.
OBO flags, environment configuration and delegation arguments are forbidden.

OCTO_MCP_REQUEST_LOG_DIR must name a pre-created private POSIX directory (0700).
Every admitted tool request is projected to the closed request-only schema and
fsynced before execution. Logging failures block business execution.

Production consumes the opt-in verify-bot policy response. Operators can select
--policy-debug --policy-debug-file /absolute/private/policy.json to replace only
the policy source; authentication and durable logging remain mandatory. The
0600 file is validated once at startup; restart to change the snapshot.

Serve-time --dry-run is rejected locally. execute.dry_run=true previews the
business request after real authentication, policy checks and request logging.
See docs/mcp-context-admission.md for wire and compatibility details.`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"skipValidation": "true"},
		RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
			if forceOnBehalfOf != "" || os.Getenv("OCTO_FORCE_ON_BEHALF_OF") != "" {
				return output.ErrValidation("OBO_FORBIDDEN", "")
			}
			if f.Globals.DryRun {
				return output.ErrValidation("MCP_OFFLINE_POLICY_UNAVAILABLE", "")
			}
			if !policyDebug && cmd.Flags().Changed("policy-debug-file") || policyDebug && policyDebugFile == "" {
				return output.ErrValidation("DEBUG_POLICY_FLAG_INVALID", "")
			}
			var debug *mcp.DebugPolicy
			if policyDebug {
				var err error
				debug, err = mcp.LoadDebugPolicyFile(policyDebugFile)
				if err != nil {
					return output.ErrValidation(err.Error(), "")
				}
			}
			fac, ok := mcp.ParseFacade(facade)
			if !ok {
				return output.ErrValidation(fmt.Sprintf("invalid --facade %q", facade), "choose one of: three, two, both")
			}
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
			build := NewRootCmd
			admission, err := mcp.NewAdmission(os.Getenv("OCTO_MCP_REQUEST_LOG_DIR"), debug)
			if err != nil {
				return output.ErrValidation(err.Error(), "")
			}
			defer func() { runErr = errors.Join(runErr, admission.Close()) }()

			if httpAddr != "" {
				return serveHTTP(cmd.Context(), httpAddr, build, tc, base, f.IOStreams.ErrOut, facade, admission)
			}
			srv, err := mcp.NewServer(build)
			if err != nil {
				return err
			}
			var downloads *mcp.SecretDownloads
			if !base.DryRun {
				downloads = mcp.SecretDownloadsFromEnv()
			}
			defer downloads.Close()
			srv.WithAdmission(admission).WithSecretDownloads(downloads).WithFacade(fac).WithTrustedContext(tc).WithBaseGlobals(base).
				WithUploadPolicy(os.Getenv("OCTO_MCP_UPLOAD_ROOT"), allowLocalUpload)
			return srv.ServeStdio(cmd.Context(), f.IOStreams.In, f.IOStreams.Out)
		},
	}
	serve.Flags().StringVar(&httpAddr, "http", "", "serve HTTP (JSON-RPC over POST) on this address (e.g. :8080); default transport is stdio")
	serve.Flags().StringVar(&forceChannelID, "force-channel-id", "", "force this channel_id on session-bound message ops (stdio + HTTP)")
	serve.Flags().StringVar(&forceChannelType, "force-channel-type", "", "force this channel_type on session-bound message ops (stdio + HTTP)")
	serve.Flags().StringVar(&forceOnBehalfOf, "force-on-behalf-of", "", "unsupported for MCP: any nonempty delegation setting is rejected")
	serve.Flags().BoolVar(&allowLocalUpload, "allow-local-upload", false, "stdio only: allow unconfined multipart file_path uploads (trusted-local hosts); default-deny unless OCTO_MCP_UPLOAD_ROOT is set")
	serve.Flags().StringVar(&facade, "facade", "three", "tool surface: three (default), two (get_skill + execute), or both")
	serve.Flags().BoolVar(&policyDebug, "policy-debug", false, "operator only: use an immutable local policy file; real Bot authentication remains required")
	serve.Flags().StringVar(&policyDebugFile, "policy-debug-file", "", "absolute path to the private policy file; requires --policy-debug")
	return serve
}

// serveHTTP runs the HTTP (JSON-RPC over POST) server until the context is cancelled or a
// termination signal arrives, then shuts down gracefully so no request is
// dropped mid-flight. Read/Idle timeouts bound a slow or idle client;
// WriteTimeout is left unset because a legitimate --page-all response can be
// long, and per-call bounding is the operator's --timeout instead.
func serveHTTP(ctx context.Context, addr string, build mcp.RootBuilder, tc mcp.TrustedContext, base cmdutil.GlobalOptions, warn io.Writer, facade string, admission ...*mcp.Admission) error { //nolint:gocritic // capture operator globals by value for connection isolation
	fac, _ := mcp.ParseFacade(facade)
	if len(admission) == 0 || admission[0] == nil {
		return output.ErrValidation("REQUEST_LOG_STARTUP_FAILED", "")
	}
	h, err := mcp.NewHTTPHandlerWithAdmission(build, tc, base, admission[0], fac)
	if err != nil {
		return err
	}
	defer h.Close()
	if !isLoopbackAddr(addr) {
		fmt.Fprintf(warn, "warning: MCP HTTP server binding %q is not loopback and serves cleartext; terminate TLS at a proxy and/or bind 127.0.0.1\n", addr) //nolint:errcheck // best-effort stderr warning
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
