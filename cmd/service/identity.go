package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// tokenKindNotAllowed is the machine code for "the selected credential is the
// wrong kind for this operation". It is a validation error (exit 2), matching
// the existing local gate in internal/client/search_route.go — the caller has
// to switch credentials, not re-authenticate the one they have.
const tokenKindNotAllowed = "TOKEN_KIND_NOT_ALLOWED"

// applyIdentityRouting enforces x-octo-allowed-token-kinds and applies
// x-octo-mount-by-token-kind to urlPath. Operations declaring neither
// extension are returned untouched and never resolve a credential here, so
// every pre-existing domain keeps its byte-identical path and its historical
// credential-resolution timing.
//
// Errors are already-classified *output.ExitError values:
//
//   - missing / ambiguous / unreadable credential → auth or validation, as the
//     credential chain classified it (exit 3 / 2)
//   - credential kind outside the allowed list, or absent from the mount table
//     → validation, TOKEN_KIND_NOT_ALLOWED (exit 2)
func applyIdentityRouting(f *cmdutil.Factory, d *registry.OperationDetail, urlPath string) (string, error) {
	if len(d.AllowedTokenKinds) == 0 && len(d.MountByTokenKind) == 0 {
		return urlPath, nil
	}

	kind, err := activeTokenKind(f)
	if err != nil {
		return "", err
	}

	if len(d.AllowedTokenKinds) > 0 && !containsString(d.AllowedTokenKinds, kind) {
		return "", tokenKindError(kind, d.AllowedTokenKinds)
	}
	if len(d.MountByTokenKind) == 0 {
		return urlPath, nil
	}
	mount, ok := d.MountByTokenKind[kind]
	if !ok {
		return "", tokenKindError(kind, mountKinds(d.MountByTokenKind))
	}
	return swapMount(urlPath, mount, d.MountByTokenKind)
}

// activeTokenKind resolves the credential and classifies it by token prefix.
// Resolution is cached on the Factory, and root's PersistentPreRunE has already
// forced it via cfg.Validate, so this adds no I/O — but ErrNoCredential must be
// translated here because buildConfig deliberately swallows it.
func activeTokenKind(f *cmdutil.Factory) (string, error) {
	cred, err := f.Credential()
	if err != nil {
		if errors.Is(err, credential.ErrNoCredential) {
			return "", output.ErrAuth(
				"no credential configured",
				"set OCTO_TOKEN (or OCTO_BOT_TOKEN), or select a stored profile with --bot-id / --profile")
		}
		return "", err
	}
	if cred == nil || cred.Token == "" {
		return "", output.ErrAuth(
			"no credential configured",
			"set OCTO_TOKEN (or OCTO_BOT_TOKEN), or select a stored profile with --bot-id / --profile")
	}
	return credential.TokenKind(cred.Token), nil
}

// tokenKindError reports an incompatible credential kind. The message names the
// kind actually in use so an Agent holding several credentials can tell which
// one it picked up.
func tokenKindError(kind string, allowed []string) *output.ExitError {
	got := kind
	if got == "" {
		got = "none"
	}
	return output.ErrWithHint(
		"validation",
		tokenKindNotAllowed,
		fmt.Sprintf("credential kind %q cannot run this operation (allowed: %s)", got, strings.Join(allowed, ", ")),
		"select a compatible credential with --bot-id / --profile, or set OCTO_TOKEN; `octo-cli auth list` shows stored profiles",
	)
}

// swapMount replaces the mount prefix the spec path is written with by the
// mount for the active token kind.
//
// The spec paths carry a full, real path (e.g. /v1/bot/drive/spaces) rather
// than a bare suffix, so `octo-cli schema` stays readable and the default
// token kind takes a zero-rewrite path. Which leading segment is the mount is
// therefore derived: exactly one value in the table must prefix the path, and
// the longest such match wins. A path that matches none is a spec bug and fails
// loudly rather than silently reaching a wrong URL.
func swapMount(urlPath, target string, table map[string]string) (string, error) {
	current := ""
	for _, candidate := range table {
		if !strings.HasPrefix(urlPath, candidate) {
			continue
		}
		if len(candidate) > len(current) {
			current = candidate
		}
	}
	if current == "" {
		return "", output.ErrWithHint(
			"internal",
			"MOUNT_PREFIX_UNKNOWN",
			fmt.Sprintf("operation path %q does not start with any x-octo-mount-by-token-kind prefix", urlPath),
			"operation-spec bug: write the path with one of the declared mounts",
		)
	}
	if current == target {
		return urlPath, nil
	}
	return target + strings.TrimPrefix(urlPath, current), nil
}

// mountKinds lists the token kinds a mount table covers, sorted so error
// messages are stable.
func mountKinds(table map[string]string) []string {
	kinds := make([]string, 0, len(table))
	for k := range table {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// MountForOperation resolves the server mount prefix that operationID's spec
// declares for the active credential's token kind, applying the same
// allowed-token-kinds gate the generated leaf commands use.
//
// Hand-written composite commands (drive upload file / download file / share
// create|blob-create|access|download) call this so they route and gate through
// exactly the same metadata as their generated siblings — there is no second
// copy of the mount table anywhere in the CLI.
func MountForOperation(f *cmdutil.Factory, operationID string) (string, error) {
	reg := f.Registry()
	if reg == nil {
		return "", output.ErrWithHint("internal", "REGISTRY_UNAVAILABLE",
			"operation registry is not available", "")
	}
	d, ok := reg.GetOperation(operationID)
	if !ok {
		return "", output.ErrWithHint("internal", "OPERATION_UNKNOWN",
			fmt.Sprintf("operation %q is not in the embedded registry", operationID),
			"CLI build bug: the composite command references a missing spec operation")
	}
	kind, err := activeTokenKind(f)
	if err != nil {
		return "", err
	}
	if len(d.AllowedTokenKinds) > 0 && !containsString(d.AllowedTokenKinds, kind) {
		return "", tokenKindError(kind, d.AllowedTokenKinds)
	}
	mount, ok := d.MountByTokenKind[kind]
	if !ok {
		return "", tokenKindError(kind, mountKinds(d.MountByTokenKind))
	}
	return mount, nil
}

// ValidateRequestBody applies operationID's declared pre-send body checks —
// required fields, minItems, enum vocabularies and uint64 id ranges — to a body
// a hand-written command assembled itself.
//
// A composite that replaces a generated leaf (drive share blob-create) or that
// posts to the same endpoint under another name (drive share create's blob
// branch) registers its own flags, so it does not inherit the engine's flag
// validation. Calling this keeps the spec the single source of the vocabulary
// either way: an out-of-enum --permission fails with ENUM_NOT_ALLOWED and exit 2
// before any HTTP, exactly as it does on the generated path, instead of the
// hand-written command sending it and letting the backend decide.
//
// flagFor maps a wire field name to the flag the caller typed, so the error
// names --permission rather than the JSON key behind it. Fields a composite
// takes positionally are simply absent from the map and are labelled by path.
func ValidateRequestBody(f *cmdutil.Factory, operationID string, body map[string]any, flagFor map[string]string) error {
	reg := f.Registry()
	if reg == nil {
		return output.ErrWithHint("internal", "REGISTRY_UNAVAILABLE",
			"operation registry is not available", "")
	}
	d, ok := reg.GetOperation(operationID)
	if !ok {
		return output.ErrWithHint("internal", "OPERATION_UNKNOWN",
			fmt.Sprintf("operation %q is not in the embedded registry", operationID),
			"CLI build bug: the composite command references a missing spec operation")
	}
	if d.RequestBody == nil {
		return nil
	}
	v := bodySchemaValidator{flagFor: flagFor}
	if exitErr := v.validate(d.RequestBody, body, "", ""); exitErr != nil {
		return exitErr
	}
	return nil
}

// RemoveLeaf detaches a generated leaf command by name from parent and reports
// whether it was there. Composite commands that must own a name the spec also
// exposes as a raw endpoint (drive share access / download / blob-create) call
// this before registering their own version, so the spec keeps documenting the
// endpoint for `octo-cli schema` while the command tree stays at one leaf per
// name.
func RemoveLeaf(parent *cobra.Command, name string) bool {
	if parent == nil {
		return false
	}
	for _, c := range parent.Commands() {
		if c.Name() == name {
			parent.RemoveCommand(c)
			return true
		}
	}
	return false
}

// FindChild returns parent's subcommand with the given name, or nil. Exposed so
// hand-written commands can attach themselves under a generated subtree.
func FindChild(parent *cobra.Command, name string) *cobra.Command {
	return findChild(parent, name)
}
