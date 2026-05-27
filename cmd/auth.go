package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// newAuthCmd returns `octo auth` and its subcommands. These manage stored bot
// credentials; tokens enter via stdin or a hidden prompt and are never accepted
// on argv, so they don't leak into shell history or transcripts.
func newAuthCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage stored bot credentials (profiles)",
	}
	cmd.AddCommand(
		newAuthLoginCmd(f),
		newAuthStatusCmd(f),
		newAuthLogoutCmd(f),
		newAuthListCmd(f),
	)
	return cmd
}

func newAuthLoginCmd(f *cmdutil.Factory) *cobra.Command {
	var withToken bool
	var tokenFile string
	var apiBaseURL string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store a bot token (read from a hidden prompt or stdin, never argv)",
		Long: "Store a bot token under a profile. Identify the bot with --bot-id " +
			"(its robot id, recommended) and/or --profile (a friendly name). The " +
			"token is read from a hidden prompt on a terminal, or from stdin with " +
			"--with-token, or from --token-file — never from the command line.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			botID := f.Globals.BotID
			name := f.Globals.Profile

			if botID == "" && name == "" {
				id, perr := promptBotID(f)
				if perr != nil {
					return failErr(f, output.ErrValidation(fmt.Sprintf("read bot id: %v", perr), ""))
				}
				botID = id
			}
			if name == "" {
				name = botID // profile name defaults to the robot id
			}
			if name == "" {
				return failErr(f, output.ErrValidation(
					"no profile identifier given",
					"pass --bot-id <robot_id> (recommended) and/or --profile <name>"))
			}

			token, err := readToken(f, withToken, tokenFile)
			if err != nil {
				return failErr(f, err)
			}
			if token == "" {
				return failErr(f, output.ErrValidation(
					"empty token", "provide a non-empty app_* or bf_* bot token"))
			}

			store, err := f.AuthStore()
			if err != nil {
				return failErr(f, err)
			}
			if err := assertBotIDFree(store, botID, name); err != nil {
				return failErr(f, err)
			}
			meta := authstore.ProfileMeta{
				APIBaseURL: apiBaseURL,
				SpaceID:    f.Globals.Space,
				BotKind:    credential.TokenKind(token),
				RobotID:    botID,
				CreatedAt:  time.Now().UTC().Format(time.RFC3339),
			}
			if err := store.SaveProfile(name, &meta, token); err != nil {
				return failErr(f, err)
			}

			count, _ := store.Count() //nolint:errcheck // advisory count for the hint only
			payload := map[string]any{
				"profile":       name,
				"bot_kind":      credential.TokenKind(token),
				"bot_token":     credential.MaskToken(token),
				"profile_count": count,
			}
			putIfSet(payload, "robot_id", botID)
			if count >= 2 {
				payload["hint"] = "multiple profiles stored; pass --bot-id or --profile on subsequent commands"
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				return failErr(f, err)
			}
			return f.EmitSuccess(raw)
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read the token from stdin instead of a prompt")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "read the token from this file")
	cmd.Flags().StringVar(&apiBaseURL, "api-base-url", "", "override OCTO_API_BASE_URL for this profile")
	cmd.MarkFlagsMutuallyExclusive("with-token", "token-file")
	return cmd
}

func newAuthStatusCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the single active bot identity (whoami); use `auth list` to enumerate",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := f.AuthStore()
			if err != nil {
				return failErr(f, err)
			}
			name, meta, status, err := store.ActiveProfile(f.Globals.Profile, selectorBotID(f))
			if err != nil {
				return failErr(f, err)
			}
			// status answers a single question — "who would I act as right now?".
			// It never enumerates profiles (that is `auth list`): the active
			// identity, or null with a pointer when nothing is selected.
			switch status {
			case authstore.StatusFound:
				token, terr := store.GetToken(name)
				if terr != nil {
					return failErr(f, terr)
				}
				active := map[string]any{
					"profile":   name,
					"bot_token": credential.MaskToken(token),
					"source":    "profile:" + name,
				}
				putIfSet(active, "robot_id", meta.RobotID)
				putIfSet(active, "bot_kind", meta.BotKind)
				putIfSet(active, "api_base_url", meta.APIBaseURL)
				putIfSet(active, "space_id", meta.SpaceID)
				return emitJSON(f, map[string]any{"active": active})
			case authstore.StatusAmbiguous:
				count, _ := store.Count() //nolint:errcheck // advisory count for the hint only
				return emitJSON(f, map[string]any{
					"active":        nil,
					"profile_count": count,
					"hint":          "no bot selected; pass --bot-id <id> or --profile <name> (or set OCTO_BOT_ID), or run `octo auth list`",
				})
			case authstore.StatusMissing:
				return failErr(f, output.ErrAuth(
					"no matching profile", "check `octo auth list`"))
			default: // none
				return emitJSON(f, map[string]any{
					"active":        nil,
					"profile_count": 0,
					"env_token_set": os.Getenv(config.EnvBotToken) != "",
					"hint":          "no stored profiles; run `octo auth login`",
				})
			}
		},
	}
}

func newAuthLogoutCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove a stored bot credential",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := f.AuthStore()
			if err != nil {
				return failErr(f, err)
			}
			name, _, status, err := store.ActiveProfile(f.Globals.Profile, selectorBotID(f))
			if err != nil {
				return failErr(f, err)
			}
			switch status {
			case authstore.StatusFound:
				if rerr := store.RemoveProfile(name); rerr != nil {
					return failErr(f, rerr)
				}
				return emitJSON(f, map[string]any{"removed": name})
			case authstore.StatusAmbiguous:
				return failErr(f, output.ErrValidation(
					"multiple profiles configured; specify which to remove",
					"pass --bot-id <robot_id> or --profile <name>"))
			case authstore.StatusMissing:
				return failErr(f, output.ErrValidation(
					"no matching profile to remove", "check `octo auth list`"))
			default: // none
				return failErr(f, output.ErrValidation(
					"no stored profiles", "nothing to remove"))
			}
		},
	}
}

func newAuthListCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List stored bot credentials (never shows tokens)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := f.AuthStore()
			if err != nil {
				return failErr(f, err)
			}
			profiles, err := store.LoadProfiles()
			if err != nil {
				return failErr(f, err)
			}
			entries := profileEntries(profiles)
			payload := map[string]any{"profiles": entries, "count": len(entries)}
			if len(entries) >= 2 {
				payload["hint"] = "multiple profiles; pass --bot-id or --profile to select one"
			}
			return emitJSON(f, payload)
		},
	}
}

// promptBotID asks for the bot id interactively when no selector was given and
// stdin is a terminal (visible — the id is not a secret). Non-terminals return
// "" so scripted/agent use still requires an explicit --bot-id / --profile.
func promptBotID(f *cmdutil.Factory) (string, error) {
	if !isTerminal(f.IOStreams.In) {
		return "", nil
	}
	writeUI(f.IOStreams.ErrOut, "Bot id (robot id, e.g. cli_xxx): ")
	return readLineVisible(f.IOStreams.In)
}

// assertBotIDFree rejects storing a bot id already owned by a different profile,
// which would make --bot-id selection ambiguous. Re-login to the same profile
// (owner == name) is allowed.
func assertBotIDFree(store *authstore.Store, botID, name string) error {
	if botID == "" {
		return nil
	}
	owner, ok, err := store.ProfileForRobotID(botID)
	if err != nil {
		return err
	}
	if ok && owner != name {
		return output.ErrValidation(
			fmt.Sprintf("bot id %q is already stored under profile %q", botID, owner),
			"log in under that profile, or remove it first with `octo auth logout`")
	}
	return nil
}

// selectorBotID returns the --bot-id flag, falling back to OCTO_BOT_ID — the
// same selection source the runtime credential chain uses.
func selectorBotID(f *cmdutil.Factory) string {
	if f.Globals.BotID != "" {
		return f.Globals.BotID
	}
	return os.Getenv(config.EnvBotID)
}

// readToken obtains the token without ever touching argv: from --token-file, or
// stdin with --with-token, or a hidden terminal prompt otherwise.
func readToken(f *cmdutil.Factory, withToken bool, tokenFile string) (string, error) {
	switch {
	case tokenFile != "":
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", output.ErrValidation(
				fmt.Sprintf("read token file: %v", err), "check the path passed to --token-file")
		}
		return strings.TrimSpace(string(data)), nil

	case withToken:
		data, err := io.ReadAll(f.IOStreams.In)
		if err != nil {
			return "", output.ErrValidation(
				fmt.Sprintf("read token from stdin: %v", err), "pipe the token into the command")
		}
		return strings.TrimSpace(string(data)), nil

	default:
		if isTerminal(f.IOStreams.In) {
			writeUI(f.IOStreams.ErrOut, "Paste bot token: ")
			tok, err := readPasswordMasked(f.IOStreams.In.(*os.File), f.IOStreams.ErrOut)
			if err != nil {
				return "", output.ErrValidation(
					fmt.Sprintf("read token: %v", err), "")
			}
			return strings.TrimSpace(tok), nil
		}
		return "", output.ErrValidation(
			"no terminal available for interactive token entry",
			"pass --with-token to read the token from stdin, or --token-file <path>")
	}
}

// escape-sequence parser states for readPasswordMasked.
const (
	escNormal   = iota // not in an escape sequence
	escAfterEsc        // saw ESC, awaiting the sequence introducer
	escInCSI           // inside a CSI/SS3 body, awaiting the final byte
)

// advanceEsc tracks a terminal escape sequence (CSI/SS3). Given the current
// state and the next byte, it returns the new state and whether the byte was
// part of a sequence — such bytes (ESC, arrow keys, bracketed-paste markers like
// ESC[200~) must never be captured into the token.
func advanceEsc(state int, c byte) (newState int, inSequence bool) {
	switch state {
	case escAfterEsc:
		// ESC '[' (CSI) or 'O' (SS3) opens a sequence; anything else was a
		// lone/Alt key — drop just this byte.
		if c == '[' || c == 'O' {
			return escInCSI, true
		}
		return escNormal, true
	case escInCSI:
		if c >= 0x40 && c <= 0x7e { // final byte ends the sequence
			return escNormal, true
		}
		return escInCSI, true
	default:
		if c == 0x1b { // ESC starts a sequence
			return escAfterEsc, true
		}
		return escNormal, false
	}
}

// applyMaskByte applies a non-escape byte to the password buffer, echoing a '*'
// (or erasing one on backspace). It reports done=true when input ends — Enter
// (abort=false) or Ctrl-C (abort=true).
func applyMaskByte(c byte, pw *[]byte, out io.Writer) (done, abort bool) {
	switch {
	case c == '\r' || c == '\n':
		return true, false
	case c == 0x03: // Ctrl-C
		return true, true
	case c == 0x7f || c == 0x08: // Backspace / Delete
		if len(*pw) > 0 {
			*pw = (*pw)[:len(*pw)-1]
			writeUI(out, "\b \b")
		}
	case c >= 0x20 && c < 0x7f: // printable ASCII only
		*pw = append(*pw, c)
		writeUI(out, "*")
	}
	return false, false
}

// readPasswordMasked reads a line from the terminal without echoing the typed
// characters, printing a '*' per keystroke as feedback. It accepts only
// printable ASCII and swallows escape sequences (see advanceEsc) so they never
// land in the token. The terminal state is restored on return; Enter ends input,
// Ctrl-C aborts.
func readPasswordMasked(file *os.File, out io.Writer) (string, error) {
	fd := int(file.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer func() { _ = term.Restore(fd, oldState) }() //nolint:errcheck // best-effort restore

	var pw []byte
	state := escNormal
	b := make([]byte, 1)
	for {
		n, rerr := file.Read(b)
		if n > 0 {
			if ns, inSeq := advanceEsc(state, b[0]); inSeq {
				state = ns
			} else if done, abort := applyMaskByte(b[0], &pw, out); done {
				writeUI(out, "\r\n")
				if abort {
					return "", errors.New("interrupted")
				}
				return string(pw), nil
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				writeUI(out, "\r\n")
				return string(pw), nil
			}
			return "", rerr
		}
	}
}

// isTerminal reports whether r is an interactive terminal.
func isTerminal(r io.Reader) bool {
	file, ok := r.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

// readLineVisible reads a single echoed line one byte at a time so it never
// buffers past the newline — leaving a subsequent hidden token read on the same
// fd intact.
func readLineVisible(r io.Reader) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			if buf[0] != '\r' {
				sb.WriteByte(buf[0])
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// putIfSet adds k=v to m only when v is non-empty, so auth output omits empty
// fields rather than emitting noisy nulls (e.g. space_id for a DM-only bot).
func putIfSet(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

// profileEntries renders the stored profiles as sorted, token-free summaries —
// shared by `auth list` and `auth status` (ambiguous case).
func profileEntries(profiles map[string]authstore.ProfileMeta) []map[string]any {
	names := make([]string, 0, len(profiles))
	for n := range profiles {
		names = append(names, n)
	}
	sort.Strings(names)

	entries := make([]map[string]any, 0, len(names))
	for _, n := range names {
		m := profiles[n]
		e := map[string]any{"profile": n}
		putIfSet(e, "robot_id", m.RobotID)
		putIfSet(e, "bot_kind", m.BotKind)
		putIfSet(e, "api_base_url", m.APIBaseURL)
		putIfSet(e, "space_id", m.SpaceID)
		putIfSet(e, "created_at", m.CreatedAt)
		entries = append(entries, e)
	}
	return entries
}

func emitJSON(f *cmdutil.Factory, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return failErr(f, err)
	}
	return f.EmitSuccess(raw)
}

// failErr writes the error envelope (to stderr, setting ErrorEmitted) and
// returns the error so main can derive the process exit code. EmitError itself
// returns nil, so RunE must return the error separately.
func failErr(f *cmdutil.Factory, err error) error {
	_ = f.EmitError(err) //nolint:errcheck // envelope write is best-effort
	return err
}

// writeUI writes an interactive prompt or echo to a terminal stream; the error
// is intentionally ignored (best-effort UI, never a command failure).
func writeUI(w io.Writer, s string) {
	_, _ = fmt.Fprint(w, s) //nolint:errcheck // best-effort interactive UI
}
