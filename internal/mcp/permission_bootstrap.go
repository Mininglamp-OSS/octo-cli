package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

// LoadPermissionHostConfig reads one private, immutable operator file. Its
// configured authority and release digest are not selectable from MCP traffic.
func LoadPermissionHostConfig(path string) (PermissionHostConfig, error) {
	var cfg PermissionHostConfig
	dir, err := secureDirectory(filepath.Dir(path), false)
	if err != nil {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	defer closePrivateFile(dir)
	f, err := openPrivatePermissionConfig(dir, filepath.Base(path))
	if err != nil {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	defer closePrivateFile(f)
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if _, err = strictObject(b, "version", "origin", "bot_uid", "recipient_id", "endpoint_incarnation", "peer_sha256", "control_address", "relay_address", "relay_url", "certificate", "private_key", "peer_ca", "release_path", "release_sha256", "authority_pins"); err != nil {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if json.Unmarshal(b, &cfg) != nil || cfg.Version != 1 {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	return cfg, nil
}

// ReadSupportedRelease requires a complete immutable release and the exact
// operator allowlisted byte digest. Candidate/index/partial artifacts refuse.
func ReadSupportedRelease(path, expected string) (permission.Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return permission.Catalog{}, errors.New("PERMISSION_RELEASE_UNAVAILABLE")
	}
	defer closePermissionIO(f)
	b, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil || len(b) > 8<<20 {
		return permission.Catalog{}, errors.New("PERMISSION_RELEASE_UNAVAILABLE")
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != expected {
		return permission.Catalog{}, errors.New("PERMISSION_RELEASE_UNAVAILABLE")
	}
	// Digest selection by an operator is necessary, not a proof of source
	// equivalence. Only a reviewed, compiled supported release can enable this
	// mode. There is currently no compatible finite release at the pinned source.
	supported, ok := supportedPermissionReleases[expected]
	if !ok || !bytes.Equal(b, supported.bytes) {
		return permission.Catalog{}, errors.New("PERMISSION_RELEASE_SOURCE_BOUNDS_UNPROVEN")
	}
	result := supported.catalog
	result.Capabilities = append([]string(nil), result.Capabilities...)
	return result, nil
}

// ServePermissionHost runs the private authenticated control and turn relay
// inside the existing MCP host process. No second unmanaged endpoint is opened.
func ServePermissionHost(ctx context.Context, cfg PermissionHostConfig, token string, build RootBuilder, admission *Admission, facade facadeMode, base cmdutil.GlobalOptions, uploadRoot string, downloads *SecretDownloads) error { //nolint:gocritic // immutable protocol/config snapshots must be detached by value
	catalog, err := ReadSupportedRelease(cfg.ReleasePath, cfg.ReleaseSHA256)
	if err != nil {
		return err
	}
	policies, err := permission.NewSelfPolicyReader(cfg.Origin, cfg.BotUID, token, nil, cfg.Origin)
	if err != nil {
		return err
	}
	native, err := permission.NewNativeReader(policies, cfg.AuthorityPins)
	if err != nil {
		return err
	}
	coordinator, err := permission.NewCoordinator(catalog, policies, native)
	if err != nil {
		return err
	}
	if err := coordinator.ConfigurePageBounds(supportedPermissionReleases[catalog.Digest].pageBounds); err != nil {
		return err
	}
	prototype, err := NewServer(build)
	if err != nil {
		return err
	}
	server := func(turn *ManagedTurn) *Server {
		s := &Server{reg: prototype.reg, mapping: prototype.mapping, build: prototype.build, protocolVersion: defaultProtocolVersion}
		s.WithManagedTurn(turn).WithAdmission(admission).WithFacade(facade).WithBaseGlobals(base).WithUploadPolicy(uploadRoot, false).WithSecretDownloads(downloads)
		s.trusted = TrustedContext{SpaceID: turn.source.SpaceID}
		s.credentialToken = token
		s.apiOrigin = cfg.Origin
		s.httpMode = true
		s.factoryFn = func(TrustedContext) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
			f, out, errout := s.makeFactory(s.trusted)
			f.ConfigFunc = func() (*config.Config, error) { return &config.Config{APIBaseURL: cfg.Origin}, nil }
			f.CredentialFunc = func() (*credential.BotCredential, error) {
				return &credential.BotCredential{Token: token, SpaceID: turn.source.SpaceID, BotKind: credential.TokenKind(token)}, nil
			}
			return f, out, errout
		}
		return s
	}
	host, err := NewPermissionHost(cfg, coordinator, policies, native, server)
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(cfg.Certificate, cfg.PrivateKey)
	if err != nil {
		return errors.New("PERMISSION_CONTROL_CERTIFICATE_UNAVAILABLE")
	}
	ca, err := os.ReadFile(cfg.PeerCA)
	if err != nil {
		return errors.New("PERMISSION_CONTROL_CERTIFICATE_UNAVAILABLE")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("PERMISSION_CONTROL_CERTIFICATE_UNAVAILABLE")
	}
	control, err := tls.Listen("tcp", cfg.ControlAddress, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots})
	if err != nil {
		return err
	}
	defer closePermissionIO(control)
	relay, err := net.Listen("tcp", cfg.RelayAddress)
	if err != nil {
		return err
	}
	defer closePermissionIO(relay)
	return host.serve(ctx, control, relay)
}

func (h *PermissionHost) serve(ctx context.Context, control, relay net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	result := make(chan error, 2)
	httpServer := &http.Server{Handler: h, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 120 * time.Second, IdleTimeout: 5 * time.Minute}
	wg.Add(2)
	go func() { defer wg.Done(); result <- httpServer.Serve(relay) }()
	go func() {
		defer wg.Done()
		for {
			conn, err := control.Accept()
			if err != nil {
				result <- err
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { mu.Lock(); delete(connections, conn); mu.Unlock(); closePermissionIO(conn) }()
				tlsConn, ok := conn.(*tls.Conn)
				if ok {
					if controlErr := h.ServeControl(ctx, tlsConn); controlErr != nil {
						return
					}
				}
			}()
		}
	}()
	var err error
	select {
	case <-ctx.Done():
	case err = <-result:
	}
	cancel()
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	closePermissionIO(control)
	closePermissionIO(relay)
	closePermissionIO(httpServer)
	mu.Lock()
	for conn := range connections {
		closePermissionIO(conn)
	}
	mu.Unlock()
	wg.Wait()
	h.handlers.Wait()
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func closePermissionIO(c io.Closer) { _ = c.Close() } //nolint:errcheck // cleanup or a disconnected protocol sink cannot be repaired by replay

type supportedPermissionRelease struct {
	bytes      []byte
	catalog    permission.Catalog
	pageBounds map[string]permission.PageBound
}

// No synthetic, partial, identity-only, or over-limit source artifact is a
// supported release. Add only complete source-compatible reviewed releases.
var supportedPermissionReleases = map[string]supportedPermissionRelease{}
