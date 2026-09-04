package credential

import (
	"fmt"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// FileProvider resolves a bot credential from the encrypted on-disk store,
// selecting by --bot-id (robot id) and/or --profile (friendly name). It is the
// stored-profile source. The command factory decides whether an explicit
// selector requires this provider or an environment credential wins first.
//
// Resolution outcomes (from authstore.ActiveProfile):
//   - found     → the stored credential
//   - missing   → a selector was given but matched nothing/was inconsistent →
//     hard error (auth, exit 3); the chain stops rather than silently using env
//   - ambiguous → ≥2 profiles and no selector → hard error (validation, exit 2)
//   - none      → 0 profiles → (nil, nil) so the caller may continue
type FileProvider struct {
	Store           *authstore.Store
	ExplicitProfile string
	ExplicitBotID   string
}

// NewFileProvider builds a FileProvider for the given selectors.
func NewFileProvider(store *authstore.Store, profile, botID string) *FileProvider {
	return &FileProvider{Store: store, ExplicitProfile: profile, ExplicitBotID: botID}
}

// Name implements Source.
func (p *FileProvider) Name() string { return "profile" }

// Resolve implements Source.
func (p *FileProvider) Resolve() (*BotCredential, error) {
	if p.Store == nil {
		return nil, nil
	}
	name, meta, status, err := p.Store.ActiveProfile(p.ExplicitProfile, p.ExplicitBotID)
	if err != nil {
		return nil, err
	}
	switch status {
	case authstore.StatusFound:
		token, err := p.Store.GetToken(name)
		if err != nil {
			return nil, err
		}
		return &BotCredential{
			Token:   token,
			SpaceID: meta.SpaceID,
			Source:  "profile:" + name,
			Profile: name,
			RobotID: meta.RobotID,
			BotKind: meta.BotKind,
		}, nil
	case authstore.StatusMissing:
		return nil, p.missingErr()
	case authstore.StatusAmbiguous:
		return nil, output.ErrValidation(
			"multiple profiles configured; specify which bot to act as",
			"pass --bot-id <robot_id> or --profile <name> (see `octo-cli auth list`)")
	default: // StatusNone
		return nil, nil
	}
}

func (p *FileProvider) missingErr() error {
	switch {
	case p.ExplicitProfile != "" && p.ExplicitBotID != "":
		return output.ErrAuth(
			fmt.Sprintf("profile %q does not match bot id %q", p.ExplicitProfile, p.ExplicitBotID),
			"check `octo-cli auth list`; the profile's robot id differs from --bot-id")
	case p.ExplicitBotID != "":
		return output.ErrAuth(
			fmt.Sprintf("no profile found for bot id %q", p.ExplicitBotID),
			"run `octo-cli auth login --bot-id "+p.ExplicitBotID+"` or check `octo-cli auth list`")
	default:
		return output.ErrAuth(
			fmt.Sprintf("profile %q not found", p.ExplicitProfile),
			"run `octo-cli auth login --profile "+p.ExplicitProfile+"` or check `octo-cli auth list`")
	}
}
