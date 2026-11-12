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
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	fields, err := strictObjectWithOptional(b, []string{"version", "origin", "bot_uid", "recipient_id", "endpoint_incarnation", "peer_sha256", "control_address", "relay_address", "relay_url", "certificate", "private_key", "peer_ca", "release_path", "release_sha256", "authority_pins", "ready_path", "relay_executable"}, "relay_ca", "relay_ca_sha256", "relay_peer_sha256", "relay_cert_sha256", "relay_server_name", "response_budgets")
	if err != nil {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if json.Unmarshal(b, &cfg) != nil || cfg.Version != 1 {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if rawBudgets, ok := fields["response_budgets"]; ok {
		if _, err := strictObject(rawBudgets, "max_acquisition_bytes", "max_page_items", "max_pages", "max_cumulative_bytes", "max_serialized_rpc_bytes"); err != nil || cfg.ResponseBudgets == nil || !cfg.ResponseBudgets.Valid() {
			return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
	}
	u, err := url.Parse(cfg.RelayURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(cfg.ReadyPath) {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	return cfg, nil
}

func permissionReadiness(incarnation string) ([]byte, error) {
	return permission.PrivateJSON(map[string]any{"control_version": 1, "coordinator_incarnation": incarnation})
}

// ReadSupportedRelease requires a complete immutable release and the exact
// operator allowlisted byte digest. Candidate/index/partial artifacts refuse.
func ReadSupportedRelease(path, expected string) (permission.Catalog, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	catalog, _, err := readPermissionRelease(ctx, path, expected, supportedPermissionPackages)
	return catalog, err
}

func readPermissionRelease(ctx context.Context, path, expected string, packages map[string]permission.CatalogPin) (permission.Catalog, *permission.VerifiedCatalog, error) {
	if pin, ok := packages[expected]; ok {
		if pin.Digest != expected || !filepath.IsAbs(path) || filepath.Base(path) != "manifest.json" {
			return permission.Catalog{}, nil, errors.New("PERMISSION_RELEASE_UNAVAILABLE")
		}
		baseline, err := PermissionCatalogBaseline()
		if err != nil {
			return permission.Catalog{}, nil, err
		}
		snapshot, err := permission.ReadCatalogPackageDirectory(ctx, filepath.Dir(path), pin, baseline, PermissionCatalogSlotValues)
		if err != nil {
			return permission.Catalog{}, nil, err
		}
		catalog := snapshot.Catalog()
		return catalog, snapshot, nil
	}
	catalog, err := readSupportedLegacyRelease(path, expected)
	return catalog, nil, err
}

func readSupportedLegacyRelease(path, expected string) (permission.Catalog, error) {
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

func permissionReleasePageBounds(catalog permission.Catalog, snapshot *permission.VerifiedCatalog) (map[string]permission.PageBound, error) {
	if snapshot != nil {
		return PermissionCatalogPageBounds()
	}
	return supportedPermissionReleases[catalog.Digest].pageBounds, nil
}

// ServePermissionHost runs the private authenticated control and turn relay
// inside the existing MCP host process. No second unmanaged endpoint is opened.
func ServePermissionHost(ctx context.Context, cfg PermissionHostConfig, token string, build RootBuilder, admission *Admission, facade facadeMode, base cmdutil.GlobalOptions, uploadRoot string, downloads *SecretDownloads) error { //nolint:gocritic // immutable startup inputs stay together
	return servePermissionHostWithPackages(ctx, cfg, token, build, admission, facade, base, uploadRoot, downloads, supportedPermissionPackages)
}

func servePermissionHostWithPackages(ctx context.Context, cfg PermissionHostConfig, token string, build RootBuilder, admission *Admission, facade facadeMode, base cmdutil.GlobalOptions, uploadRoot string, downloads *SecretDownloads, packages map[string]permission.CatalogPin) error { //nolint:gocritic,gocyclo // immutable startup inputs and complete verification/readiness ordering stay together
	startup, cancelStartup := context.WithTimeout(ctx, 120*time.Second)
	catalog, snapshot, err := readPermissionRelease(startup, cfg.ReleasePath, cfg.ReleaseSHA256, packages)
	cancelStartup()
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(cfg.Certificate, cfg.PrivateKey)
	if err != nil || len(cert.Certificate) == 0 {
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
	leafSum := sha256.Sum256(cert.Certificate[0])
	leafHex := hex.EncodeToString(leafSum[:])
	if (cfg.RelayCertSHA256 != "" && cfg.RelayCertSHA256 != leafHex) || (cfg.RelayPeerSHA256 != "" && cfg.RelayPeerSHA256 != leafHex) {
		return errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if cfg.RelayCA == "" {
		cfg.RelayCA = cfg.PeerCA
	}
	originRoots, err := x509.SystemCertPool()
	if err != nil || originRoots == nil {
		originRoots = x509.NewCertPool()
	}
	originRoots.AppendCertsFromPEM(ca)
	relayCABytes := ca
	if cfg.RelayCA != "" && cfg.RelayCA != cfg.PeerCA {
		var readErr error
		relayCABytes, readErr = os.ReadFile(cfg.RelayCA)
		if readErr != nil || !originRoots.AppendCertsFromPEM(relayCABytes) {
			return errors.New("PERMISSION_CONTROL_CERTIFICATE_UNAVAILABLE")
		}
	}
	caSum := sha256.Sum256(relayCABytes)
	caHex := hex.EncodeToString(caSum[:])
	if cfg.RelayCASHA256 != "" && cfg.RelayCASHA256 != caHex {
		return errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if cfg.RelayCertSHA256 == "" || cfg.RelayPeerSHA256 == "" || cfg.RelayCASHA256 == "" {
		if _, err := TurnRelayHandleWithTrust(cfg.RelayURL, strings.Repeat("a", 64), TurnRelayTrust{CAFile: cfg.RelayCA, CASHA256: caHex, PeerSHA256: leafHex, CertSHA256: leafHex, ServerName: cfg.RelayServerName}); err == nil {
			cfg.RelayCASHA256 = caHex
			cfg.RelayPeerSHA256 = leafHex
			cfg.RelayCertSHA256 = leafHex
		}
	}
	policies, err := permission.NewSelfPolicyReader(cfg.Origin, cfg.BotUID, token, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: originRoots}, cfg.Origin)
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
	if snapshot != nil {
		if err := coordinator.ConfigureCatalogPackage(snapshot); err != nil {
			return err
		}
	}
	pageBounds, err := permissionReleasePageBounds(catalog, snapshot)
	if err != nil {
		return err
	}
	if err := coordinator.ConfigurePageBounds(pageBounds); err != nil {
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
	control, err := tls.Listen("tcp", cfg.ControlAddress, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots})
	if err != nil {
		return err
	}
	defer closePermissionIO(control)
	relay, err := net.Listen("tcp", cfg.RelayAddress)
	if err != nil {
		return err
	}
	defer closePermissionIO(relay)
	cleanup, err := writePermissionReadiness(cfg.ReadyPath, host.Incarnation())
	if err != nil {
		return err
	}
	defer cleanup()
	secured := permission.PublicationTLSListener(relay, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}})
	return host.serve(ctx, control, secured)
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

// Versioned package pins are independent reviewed support inputs. Endpoint
// descriptor v2 binds release_path to this exact manifest.json and release_sha256
// to its raw canonical bytes. No format is guessed from a model or file prefix.
// Source-compatible docs/search/byte contracts and independent gates are still
// missing, so a structurally verified candidate is not added to this table.
var supportedPermissionPackages = map[string]permission.CatalogPin{}
