package cmd

import (
	"encoding/json"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const (
	mailJMAPSessionPath = "/agent-mail-api/jmap/session"
	mailJMAPAPIPath     = "/agent-mail-api/jmap/api"
	mailJMAPCapability  = "urn:ietf:params:jmap:mail"
)

type mailJMAPSession struct {
	PrimaryAccounts map[string]string `json:"primaryAccounts"`
}

type mailJMAPResponse struct {
	MethodResponses [][3]json.RawMessage `json:"methodResponses"`
}

func attachMailJMAPCommands(root *cobra.Command, f *cmdutil.Factory) {
	mail := childCommand(root, "mail")
	if mail == nil {
		return
	}
	message := childCommand(mail, "message")
	if message == nil {
		return
	}
	if childCommand(message, "state") == nil {
		message.AddCommand(newMailMessageStateCmd(f))
	}
	if childCommand(message, "changes") == nil {
		message.AddCommand(newMailMessageChangesCmd(f))
	}
}

func childCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, command := range parent.Commands() {
		if command.Name() == name {
			return command
		}
	}
	return nil
}

func newMailMessageStateCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "state",
		Short: "Get the current RFC 8621 Email state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			accountID, preview, err := mailJMAPAccountID(cmd, f)
			if err != nil {
				return failErr(f, err)
			}
			if preview != nil {
				return f.EmitSuccess(preview)
			}
			response, err := callMailJMAP(cmd, f, "Email/get", map[string]any{
				"accountId": accountID,
				"ids":       []string{},
			})
			if err != nil {
				return failErr(f, err)
			}
			state, _ := response["state"].(string)
			if state == "" {
				return failErr(f, output.ErrAPI("INVALID_JMAP_RESPONSE", "JMAP Email/get did not return state", "retry the request"))
			}
			return emitJSON(f, map[string]any{"account_id": accountID, "state": state})
		},
	}
}

func newMailMessageChangesCmd(f *cmdutil.Factory) *cobra.Command {
	var sinceState string
	var maxChanges int
	command := &cobra.Command{
		Use:   "changes",
		Short: "Read RFC 8621 Email changes after a saved state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sinceState == "" {
				return failErr(f, output.ErrValidation("--since-state is required", "run `octo-cli mail message state` once, persist that state, then poll changes"))
			}
			if maxChanges <= 0 {
				return failErr(f, output.ErrValidation("--max-changes must be positive", "use a value such as 100"))
			}
			accountID, preview, err := mailJMAPAccountID(cmd, f)
			if err != nil {
				return failErr(f, err)
			}
			if preview != nil {
				return f.EmitSuccess(preview)
			}
			response, err := callMailJMAP(cmd, f, "Email/changes", map[string]any{
				"accountId":  accountID,
				"sinceState": sinceState,
				"maxChanges": maxChanges,
			})
			if err != nil {
				return failErr(f, err)
			}
			return emitJSON(f, response)
		},
	}
	command.Flags().StringVar(&sinceState, "since-state", "", "previous JMAP Email state")
	command.Flags().IntVar(&maxChanges, "max-changes", 100, "maximum changed Email ids in one page")
	return command
}

func mailJMAPAccountID(cmd *cobra.Command, f *cmdutil.Factory) (accountID string, preview []byte, err error) {
	mailClient, err := f.ClientForCredential(cmd.Context(), "mail")
	if err != nil {
		return "", nil, err
	}
	raw, err := mailClient.Do(cmd.Context(), &client.Request{
		Method: http.MethodGet, Path: mailJMAPSessionPath, Credential: "mail", SuppressSpaceHeader: true,
	})
	if err != nil {
		return "", nil, err
	}
	// A dry-run response describes the session-discovery request; it is not a
	// JMAP Session document. Return it to the command for direct emission and
	// stop before constructing the dependent Email/get or Email/changes call,
	// whose accountId cannot be known without executing discovery.
	if f.Globals != nil && f.Globals.DryRun {
		return "", raw, nil
	}
	var session mailJMAPSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return "", nil, output.ErrAPI("INVALID_JMAP_SESSION", "could not decode JMAP session", "retry the request")
	}
	accountID = session.PrimaryAccounts[mailJMAPCapability]
	if accountID == "" {
		return "", nil, output.ErrAPI("JMAP_MAIL_UNAVAILABLE", "JMAP session has no primary Mail account", "reconnect the Agent mailbox")
	}
	return accountID, nil, nil
}

func callMailJMAP(cmd *cobra.Command, f *cmdutil.Factory, method string, arguments map[string]any) (map[string]any, error) {
	mailClient, err := f.ClientForCredential(cmd.Context(), "mail")
	if err != nil {
		return nil, err
	}
	raw, err := mailClient.Do(cmd.Context(), &client.Request{
		Method: http.MethodPost, Path: mailJMAPAPIPath, Credential: "mail", SuppressSpaceHeader: true,
		Body: map[string]any{
			"using":       []string{"urn:ietf:params:jmap:core", mailJMAPCapability},
			"methodCalls": []any{[]any{method, arguments, "mail-cli"}},
		},
	})
	if err != nil {
		return nil, err
	}
	var response mailJMAPResponse
	if err := json.Unmarshal(raw, &response); err != nil || len(response.MethodResponses) != 1 {
		return nil, output.ErrAPI("INVALID_JMAP_RESPONSE", "could not decode JMAP method response", "retry the request")
	}
	var responseName string
	var responseArgs map[string]any
	if err := json.Unmarshal(response.MethodResponses[0][0], &responseName); err != nil {
		return nil, output.ErrAPI("INVALID_JMAP_RESPONSE", "JMAP response has no method name", "retry the request")
	}
	if err := json.Unmarshal(response.MethodResponses[0][1], &responseArgs); err != nil {
		return nil, output.ErrAPI("INVALID_JMAP_RESPONSE", "JMAP response has invalid arguments", "retry the request")
	}
	if responseName == "error" {
		code, _ := responseArgs["type"].(string)
		message, _ := responseArgs["description"].(string)
		if code == "" {
			code = "JMAP_ERROR"
		}
		if message == "" {
			message = "JMAP method failed"
		}
		return nil, output.ErrAPI(code, message, "inspect the saved state and retry")
	}
	if responseName != method {
		return nil, output.ErrAPI("INVALID_JMAP_RESPONSE", "JMAP returned an unexpected method response", "retry the request")
	}
	return responseArgs, nil
}
