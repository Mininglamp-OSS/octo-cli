package mcp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in, offline exact-source consumer test. The environment selects only an
// already-downloaded file whose bytes must match the pinned endpoint source.
func TestEndpointE460ControlAndActualTurnRelayExecutable(t *testing.T) {
	sourcePath := os.Getenv("XIN2794_ENDPOINT_CONTROL_SOURCE")
	if sourcePath == "" {
		t.Skip("exact endpoint e4601041 source not supplied for offline cross-repository fixture")
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil || sha256Sum(source) != "e390188ce11fc5c22041feff24119f81b7fa3a3a8efb6555b9fb7328dd676fbe" {
		t.Fatal("endpoint source pin mismatch", err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node required for opted-in endpoint consumer fixture")
	}
	f := newManagedFixture(t)
	dir := secureTestDir(t)
	sourceCopy := filepath.Join(dir, "control-client.ts")
	if err := os.WriteFile(sourceCopy, source, 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "relay-cli")
	build := exec.Command("go", "build", "-o", binary, "./cmd/octo-cli")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	serverCert, clientCert, roots, peerHash := controlCertificates(t)
	cfg := PermissionHostConfig{Version: 1, Origin: f.backend.URL, BotUID: "bot", RecipientID: "endpoint-1", EndpointIncarnation: strings.Repeat("5", 32), PeerSHA256: peerHash, RelayURL: "http://127.0.0.1", RelayExecutable: binary}
	host, err := NewPermissionHost(cfg, f.coordinator, f.policies, f.reader, f.server)
	if err != nil {
		t.Fatal(err)
	}
	relay := httptest.NewServer(host)
	defer relay.Close()
	host.config.RelayURL = relay.URL
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		done <- host.ServeControl(context.Background(), conn.(*tls.Conn))
	}()
	defer func() {
		listener.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("endpoint control did not drain")
		}
	}()
	key, err := x509.MarshalPKCS8PrivateKey(clientCert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	chain := []byte{}
	for _, raw := range clientCert.Certificate {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})...)
	}
	config := map[string]any{"port": listener.Addr().(*net.TCPAddr).Port, "host": "127.0.0.1", "servername": "localhost", "ca": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[1]})), "cert": string(chain), "key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})), "endpointIncarnation": cfg.EndpointIncarnation, "coordinatorIncarnation": host.Incarnation(), "recipientId": cfg.RecipientID, "configuredOrigin": cfg.Origin, "peerCertificateSha256": sha256Sum(serverCert.Certificate[0]), "timeoutMs": 10000}
	fixture := map[string]any{"control": config, "key": f.bundle.Key, "header": f.header}
	data, _ := json.Marshal(fixture)
	configPath := filepath.Join(dir, "fixture.json")
	os.WriteFile(configPath, data, 0o600)
	scriptPath := filepath.Join(dir, "consumer.mjs")
	os.WriteFile(scriptPath, []byte(endpointConsumerScript), 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, scriptPath, sourceCopy, configPath)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "ENDPOINT_RELAY_OK") || f.business.Load() != 1 {
		t.Fatal("actual endpoint consumer/CLI relay failed", err, string(output), f.business.Load())
	}
}

const endpointConsumerScript = `
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import {createHash,randomBytes} from 'node:crypto';
import {spawnSync} from 'node:child_process';
let source=readFileSync(process.argv[2],'utf8');
// Standard parameter-property lowering for Node's strip-only TypeScript mode.
const parameter='private constructor(private readonly socket: TLSSocket, private readonly options: ControlConnection) {';
if(!source.includes(parameter)) throw Error('Pinned parameter property changed');
source=source.replace(parameter,'private constructor(socket: TLSSocket, options: ControlConnection) { this.socket=socket; this.options=options;');
const code=stripTypeScriptTypes(source,{mode:'strip'});
const {PermissionControlClient,canonicalControl}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'));
const fixture=JSON.parse(readFileSync(process.argv[3],'utf8'));
const control=await PermissionControlClient.connect(fixture.control);
try {
const ingress=await control.call('register_ingress',{key:fixture.key,delivery_reference:'delivery',stream:fixture.header.delivery,payload_sha256:fixture.header.payload_sha256});
const context_id=randomBytes(16).toString('hex');
const admitted=await control.call('admit',{ingress_id:ingress.ingress_id,proposed_context_id:context_id});
const manifest={context_id,context_generation:admitted.generation,originals:[fixture.header]};
const bytes=Buffer.from(canonicalControl(manifest));
await control.call('bind_context',{capsule_id:admitted.capsule_id,manifest:{...manifest,bytes:bytes.length,sha256:createHash('sha256').update(bytes).digest('hex')}});
const relay=await control.call('open_relay',{capsule_id:admitted.capsule_id,recipient_id:'endpoint-1',transport:'stdio',relay_nonce:randomBytes(16).toString('hex')});
if(createHash('sha256').update(readFileSync(relay.executable)).digest('hex')!==relay.executableSha256)throw Error('Executable pin mismatch');
const input=JSON.stringify({jsonrpc:'2.0',id:1,method:'initialize',params:{capabilities:{}}})+'\n'+JSON.stringify({jsonrpc:'2.0',id:2,method:'tools/call',params:{name:'call_op',arguments:{operation_id:'message.send',arguments:{channel_id:'target',channel_type:2,payload:{text:'synthetic'}}}}})+'\n';
const worker=spawnSync(relay.executable,['--turn-relay',relay.handle],{input,encoding:'utf8',env:{},timeout:20000});
if(worker.status!==0||!worker.stdout.includes('synthetic-result'))throw Error('Worker failed: '+worker.stderr+' '+worker.stdout);
await control.call('close',{capsule_id:admitted.capsule_id,reason:'completed'});
let duplicateBlocked=false;try{await control.call('register_ingress',{key:fixture.key,delivery_reference:'delivery',stream:fixture.header.delivery,payload_sha256:fixture.header.payload_sha256});}catch{duplicateBlocked=true;}
if(!duplicateBlocked)throw Error('Native delivery fence erased after close');
console.log('ENDPOINT_RELAY_OK');
}finally {control.close();}
`
