package cmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const (
	mailDevicePath = "/agent-mail-api/webapi/v0/agent-auth/device"
	mailTokenPath  = "/agent-mail-api/webapi/v0/agent-auth/token"
)

type mailDeviceResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

type mailTokenResponse struct {
	AccessToken    string `json:"accessToken"`
	MailboxAddress string `json:"mailboxAddress"`
	BotID          string `json:"botId"`
	BotProfile     string `json:"botProfile"`
}

func attachMailAuthCmd(root *cobra.Command, f *cmdutil.Factory) {
	var mail *cobra.Command
	for _, command := range root.Commands() {
		if command.Name() == "mail" {
			mail = command
			break
		}
	}
	if mail == nil {
		return
	}
	auth := &cobra.Command{
		Use:         "auth",
		Short:       "Connect the current Bot to an Agent mailbox",
		RunE:        func(cmd *cobra.Command, args []string) error { return cmd.Help() },
		Annotations: map[string]string{"skipValidation": "true"},
	}
	auth.AddCommand(newMailAuthLoginCmd(f), newMailAuthStatusCmd(f))
	mail.AddCommand(auth)
}

func newMailAuthLoginCmd(f *cmdutil.Factory) *cobra.Command { //nolint:gocyclo // device authorization validates and persists one bounded state transition
	var mailboxAddress string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Start mailbox authorization for the current Bot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mailboxAddress = strings.TrimSpace(mailboxAddress)
			if mailboxAddress != "" && (len(mailboxAddress) > 320 || strings.Count(mailboxAddress, "@") != 1) {
				return failErr(f, output.ErrValidation(
					"invalid Agent Mail mailbox address",
					"pass the exact address shown in OCTO Web, for example agent@example.com",
				))
			}
			bot, err := selectedMailBot(cmd.Context(), f, !f.Globals.DryRun)
			if err != nil {
				return failErr(f, err)
			}
			if strings.TrimSpace(bot.RobotID) == "" {
				return failErr(f, output.ErrValidation(
					"Agent Mail authorization requires a resolved Bot id",
					"set OCTO_BOT_ID to preview locally, or run without --dry-run to resolve it",
				))
			}
			if strings.TrimSpace(bot.SpaceID) == "" {
				hint := "pass --space <space_id> or set OCTO_SPACE_ID for the active Bot"
				if bot.Profile != "" {
					hint = "pass --space <space_id> for the active stored profile"
				}
				return failErr(f, output.ErrValidation(
					"Agent Mail authorization requires a Space",
					hint,
				))
			}
			cfg, err := f.Config()
			if err != nil {
				return failErr(f, err)
			}
			verifierBytes := make([]byte, 32)
			if _, err := rand.Read(verifierBytes); err != nil {
				return failErr(f, err)
			}
			verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
			digest := sha256.Sum256([]byte(verifier))
			challenge := base64.RawURLEncoding.EncodeToString(digest[:])

			cli, err := newMailAuthorizationClient(f)
			if err != nil {
				return failErr(f, err)
			}
			body := map[string]any{
				"botId": bot.RobotID, "clientName": "octo-cli", "spaceId": bot.SpaceID,
				"codeChallenge": challenge,
			}
			if bot.Profile != "" {
				body["botProfile"] = bot.Profile
			}
			if mailboxAddress != "" {
				body["mailboxAddress"] = mailboxAddress
			}
			raw, err := cli.Do(cmd.Context(), &client.Request{
				Method: http.MethodPost, Path: mailDevicePath,
				Body:                           body,
				DisableRetry:                   true,
				UnknownOutcomeOnNetworkFailure: true,
			})
			if err != nil {
				return failErr(f, err)
			}
			// The identity prerequisite above is always executed, but --dry-run
			// must not create a device flow or persist its proof material. The
			// client returns the redacted request description as its synthetic
			// response, so emit it directly instead of parsing it as a device code.
			if f.Globals.DryRun {
				return f.EmitSuccess(raw)
			}
			var device mailDeviceResponse
			if err := json.Unmarshal(raw, &device); err != nil || device.DeviceCode == "" || device.VerificationURIComplete == "" {
				return failErr(f, output.ErrAPI("INVALID_AUTH_RESPONSE", "Agent Mail returned an invalid authorization response", "retry authorization"))
			}
			store, err := f.AuthStore()
			if err != nil {
				return failErr(f, err)
			}
			expiresAt := time.Now().UTC().Add(time.Duration(device.ExpiresIn) * time.Second)
			bindingKey, err := cmdutil.MailBindingKeyForBot(bot, cfg.APIBaseURL)
			if err != nil {
				return failErr(f, err)
			}
			if err := store.SavePendingMailAuthorization(bindingKey, &authstore.PendingMailAuthorization{
				DeviceCode: device.DeviceCode, CodeVerifier: verifier,
				UserCode: device.UserCode, VerificationURI: device.VerificationURIComplete,
				ExpiresAt: expiresAt.Format(time.RFC3339),
			}); err != nil {
				return failErr(f, err)
			}
			result := map[string]any{
				"status": "authorization_required", "bot_id": bot.RobotID,
				"user_code":        device.UserCode,
				"verification_uri": device.VerificationURIComplete,
				"expires_at":       expiresAt.Format(time.RFC3339),
				"next":             "Ask the user to approve this URL, then run `octo-cli mail auth status`.",
			}
			if mailboxAddress != "" {
				result["requested_mailbox_address"] = mailboxAddress
			}
			addMailProfile(result, bot.Profile)
			return emitJSON(f, result)
		},
	}
	cmd.Flags().StringVar(&mailboxAddress, "mailbox", "", "preselect this mailbox for human approval")
	return cmd
}

func newMailAuthStatusCmd(f *cmdutil.Factory) *cobra.Command { //nolint:gocyclo // status handles the explicit device-flow state machine
	return &cobra.Command{
		Use:   "status",
		Short: "Complete a pending authorization or show the current connection",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bot, err := selectedMailBot(cmd.Context(), f, false)
			if err != nil {
				return failErr(f, err)
			}
			cfg, err := f.Config()
			if err != nil {
				return failErr(f, err)
			}
			store, err := f.AuthStore()
			if err != nil {
				return failErr(f, err)
			}
			pending, pendingBinding, err := storedPendingMailAuthorization(store, bot, cfg.APIBaseURL)
			if err != nil {
				if errors.Is(err, authstore.ErrMailCredentialNotFound) {
					return showCurrentMailConnection(cmd, f, store, bot, cfg)
				}
				if errors.Is(err, authstore.ErrMailCredentialAmbiguous) {
					return failErr(f, ambiguousMailBindingError())
				}
				return failErr(f, err)
			}
			applyMailBinding(bot, pendingBinding)
			pendingKey := pendingBinding.Key

			cli, err := newMailAuthorizationClient(f)
			if err != nil {
				return failErr(f, err)
			}
			raw, err := cli.Do(cmd.Context(), &client.Request{
				Method: http.MethodPost, Path: mailTokenPath,
				Body: map[string]string{
					"deviceCode": pending.DeviceCode, "codeVerifier": pending.CodeVerifier,
				},
				SecretValues:                   []string{pending.DeviceCode, pending.CodeVerifier},
				DisableRetry:                   true,
				UnknownOutcomeOnNetworkFailure: true,
			})
			if err != nil {
				if ee := output.AsExitError(err); ee != nil && ee.Code == "authorization_pending" {
					result := map[string]any{
						"status": "pending", "bot_id": bot.RobotID,
						"user_code": pending.UserCode, "verification_uri": pending.VerificationURI,
						"expires_at": pending.ExpiresAt,
					}
					addMailProfile(result, bot.Profile)
					return emitJSON(f, result)
				}
				if ee := output.AsExitError(err); ee != nil &&
					(ee.Code == "authorization_expired" || ee.Code == "authorization_used" || ee.Code == "authorization_denied") {
					_ = store.RemovePendingMailAuthorization(pendingKey) //nolint:errcheck // preserve the authoritative authorization error
				}
				return failErr(f, err)
			}
			// Preserve the pending authorization during a preview. The token
			// exchange remains a redacted request description and no prerequisite
			// identity request is sent.
			if f.Globals.DryRun {
				return f.EmitSuccess(raw)
			}
			var token mailTokenResponse
			if err := json.Unmarshal(raw, &token); err != nil || !strings.HasPrefix(token.AccessToken, "omb_") || token.MailboxAddress == "" {
				return failErr(f, output.ErrAPI("INVALID_AUTH_RESPONSE", "Agent Mail returned an invalid token response", "retry authorization"))
			}
			if token.BotID != bot.RobotID {
				return failErr(f, output.ErrAuth("authorization was issued for another Bot", "restart Agent Mail authorization"))
			}
			credentialKey, err := cmdutil.MailBindingKeyForBot(bot, cfg.APIBaseURL)
			if err != nil {
				return failErr(f, err)
			}
			if err := store.SaveMailCredential(credentialKey, token.AccessToken); err != nil {
				return failErr(f, err)
			}
			if err := store.RemovePendingMailAuthorization(pendingKey); err != nil {
				return failErr(f, err)
			}
			result := map[string]any{
				"status": "connected",
				"bot_id": bot.RobotID, "mailbox_address": token.MailboxAddress,
			}
			addMailProfile(result, bot.Profile)
			return emitJSON(f, result)
		},
	}
}

// newMailAuthorizationClient uses the unified OCTO origin without attaching a
// Bot or mailbox credential. The device and token endpoints are deliberately
// public OAuth-style bootstrap endpoints; Bot identity is verified separately
// through /v1/bot/register before a real device flow is created. A dry-run
// uses only the locally supplied identity claim and sends no request.
func newMailAuthorizationClient(f *cmdutil.Factory) (*client.Client, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, err
	}
	return client.New(cfg, nil, client.Options{
		Verbose: f.Globals.Verbose,
		DryRun:  f.Globals.DryRun,
		NoRetry: f.Globals.NoRetry,
		Timeout: f.Globals.Timeout,
		ErrOut:  f.ErrOut(),
	}), nil
}

func selectedMailBot(ctx context.Context, f *cmdutil.Factory, verify bool) (*credential.BotCredential, error) {
	if verify {
		return f.VerifyBotIdentity(ctx)
	}
	bot, err := f.Credential()
	if err != nil {
		return nil, err
	}
	return bot, nil
}

func showCurrentMailConnection(cmd *cobra.Command, f *cmdutil.Factory, store *authstore.Store, bot *credential.BotCredential, cfg *config.Config) error {
	token, binding, err := storedMailCredentialForBot(store, bot, cfg.APIBaseURL)
	if err != nil {
		if errors.Is(err, authstore.ErrMailCredentialNotFound) {
			result := map[string]any{
				"status": "unconnected", "bot_id": bot.RobotID,
				"next": "Run `octo-cli mail auth login` to start authorization.",
			}
			addMailProfile(result, bot.Profile)
			return emitJSON(f, result)
		}
		if errors.Is(err, authstore.ErrMailCredentialAmbiguous) {
			return failErr(f, ambiguousMailBindingError())
		}
		return failErr(f, err)
	}
	applyMailBinding(bot, binding)
	credentialKey := binding.Key
	mailClient := client.NewMail(cfg, &credential.MailCredential{
		Token: token, BotID: bot.RobotID, BotProfile: bot.Profile, Source: "mail-store:" + credentialKey,
	}, client.Options{
		Verbose: f.Globals.Verbose,
		DryRun:  f.Globals.DryRun,
		NoRetry: f.Globals.NoRetry,
		Timeout: f.Globals.Timeout,
		ErrOut:  f.ErrOut(),
	})
	raw, err := mailClient.Do(cmd.Context(), &client.Request{
		Method: http.MethodGet, Path: "/agent-mail-api/webapi/v0/identity",
	})
	if err != nil {
		if ee := output.AsExitError(err); ee != nil &&
			(ee.Type == "auth_error" || ee.Code == "unauthorized") {
			_ = store.RemoveMailCredential(credentialKey) //nolint:errcheck // revocation is already authoritative; local cleanup is best-effort
			result := map[string]any{
				"status": "unconnected", "bot_id": bot.RobotID,
				"next": "The previous authorization was revoked. Run `octo-cli mail auth login`.",
			}
			addMailProfile(result, bot.Profile)
			return emitJSON(f, result)
		}
		return failErr(f, err)
	}
	if f.Globals.DryRun {
		return f.EmitSuccess(raw)
	}
	var identity struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(raw, &identity); err != nil || identity.Address == "" {
		return failErr(f, output.ErrAPI("INVALID_IDENTITY_RESPONSE", "Agent Mail returned an invalid identity response", "retry status"))
	}
	result := map[string]any{
		"status": "connected", "bot_id": bot.RobotID, "mailbox_address": identity.Address,
	}
	addMailProfile(result, bot.Profile)
	return emitJSON(f, result)
}

func storedMailCredentialForBot(store *authstore.Store, bot *credential.BotCredential, apiOrigin string) (string, authstore.MailBinding, error) {
	return store.FindMailCredential(bot.RobotID, bot.SpaceID, bot.Token, apiOrigin)
}

func storedPendingMailAuthorization(store *authstore.Store, bot *credential.BotCredential, apiOrigin string) (authstore.PendingMailAuthorization, authstore.MailBinding, error) {
	return store.FindPendingMailAuthorization(bot.RobotID, bot.SpaceID, bot.Token, apiOrigin)
}

func applyMailBinding(bot *credential.BotCredential, binding authstore.MailBinding) {
	bot.RobotID = binding.RobotID
	bot.SpaceID = binding.SpaceID
}

func ambiguousMailBindingError() error {
	return output.ErrValidation(
		"multiple Agent Mail connections match the active Bot",
		"pass --space <space_id> to select one",
	)
}

func addMailProfile(result map[string]any, profile string) {
	if profile != "" {
		result["profile"] = profile
	}
}
