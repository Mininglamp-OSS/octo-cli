package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

type admissionError struct {
	code   int
	reason string
}

func (e *admissionError) Error() string              { return e.reason }
func admissionFailure(code int, reason string) error { return &admissionError{code, reason} }
func policyFailure() error                           { return admissionFailure(-32003, "POLICY_UNAVAILABLE") }
func validMode(s string) bool                        { return s == "strict" || s == "compatible" }

func parsePolicy(raw json.RawMessage) (string, error) { //nolint:gocyclo // validate each policy algebra invariant without defaulting malformed fields
	p, err := strictObject(raw, "version", "context_mode", "override", "stage_default", "source", "generation", "bot_revision")
	if err != nil {
		return "", policyFailure()
	}
	var mode, stage, source, generation string
	for k, dst := range map[string]*string{"context_mode": &mode, "stage_default": &stage, "source": &source, "generation": &generation} {
		if json.Unmarshal(p[k], dst) != nil {
			return "", policyFailure()
		}
	}
	if string(p["version"]) != "1" || !validMode(mode) || !validMode(stage) || !positiveDecimal(generation, 64) {
		return "", policyFailure()
	}
	switch source {
	case "stage_default":
		if string(p["override"]) != "null" || string(p["bot_revision"]) != "null" || mode != stage {
			return "", policyFailure()
		}
	case "bot":
		var override, revision string
		if json.Unmarshal(p["override"], &override) != nil || json.Unmarshal(p["bot_revision"], &revision) != nil || !validMode(override) || override != mode || !positiveDecimal(revision, 64) {
			return "", policyFailure()
		}
		g, ge := strconv.ParseUint(generation, 10, 64)
		r, re := strconv.ParseUint(revision, 10, 64)
		if ge != nil || re != nil {
			return "", policyFailure()
		}
		if r > g {
			return "", policyFailure()
		}
	default:
		return "", policyFailure()
	}
	return mode, nil
}

// verifyMCPBot uses one bounded, read-only verification. No registration,
// identity cache, policy cache, retry, verbose output, or fallback is involved.
func verifyMCPBot(ctx context.Context, f *cmdutil.Factory, requestPolicy bool) (uid, origin, mode string, err error) { //nolint:gocyclo // one bounded verification maps distinct auth, transport and policy failures
	bot, e := f.Credential()
	if e != nil || bot == nil || bot.Token == "" {
		err = admissionFailure(-32001, "BOT_AUTH_REQUIRED")
		return
	}
	if !strings.HasPrefix(bot.Token, "bf_") {
		err = admissionFailure(-32001, "BOT_CREDENTIAL_UNSUPPORTED")
		return
	}
	cfg, e := f.Config()
	if e != nil || cfg == nil {
		err = admissionFailure(-32002, "BOT_VERIFICATION_UNAVAILABLE")
		return
	}
	origin, e = config.NormalizeAPIBaseURL(cfg.APIBaseURL)
	if e != nil {
		err = admissionFailure(-32002, "BOT_VERIFICATION_UNAVAILABLE")
		return
	}
	cc, bc := *cfg, *bot
	cc.APIBaseURL = origin
	f.ConfigFunc = func() (*config.Config, error) { c := cc; return &c, nil }
	f.CredentialFunc = func() (*credential.BotCredential, error) { c := bc; return &c, nil }
	body := map[string]any{"bot_token": bc.Token}
	if requestPolicy {
		body["include_mcp_policy"] = true
	}
	vctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c := client.New(&cc, &bc, client.Options{Timeout: "3s", NoRetry: true, ErrOut: io.Discard})
	raw, e := c.Do(vctx, &client.Request{Method: http.MethodPost, Path: "/v1/auth/verify-bot", Body: body, DisableRetry: true, SuppressSpaceHeader: true, SecretValues: []string{bc.Token}, SensitiveJSONFields: []string{"bot_token"}, MaxResponseBytes: 16 << 10})
	if e != nil {
		err = admissionFailure(-32002, "BOT_VERIFICATION_UNAVAILABLE")
		if ee := output.AsExitError(e); ee != nil {
			if ee.HTTPStatus == 401 || ee.Code == "HTTP_401" || ee.Code == "AUTH_ERROR" {
				err = admissionFailure(-32001, "BOT_AUTH_INVALID")
			}
			if ee.Code == "err.server.mcp.policy_unavailable" {
				err = policyFailure()
			}
		}
		return
	}
	p, e := strictObject(raw)
	if e != nil {
		err = admissionFailure(-32002, "BOT_VERIFICATION_UNAVAILABLE")
		return
	}
	if json.Unmarshal(p["bot_uid"], &uid) != nil || !validID(uid, 40) || strings.Contains(uid, bc.Token) {
		err = admissionFailure(-32002, "BOT_VERIFICATION_UNAVAILABLE")
		return
	}
	if e = f.BindVerifiedBotIdentity(bc.Token, origin, uid); e != nil {
		err = admissionFailure(-32001, "BOT_AUTH_INVALID")
		return
	}
	if requestPolicy {
		mode, err = parsePolicy(p["mcp_policy"])
	}
	return
}
