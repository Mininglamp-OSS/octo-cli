package authstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
)

const (
	mailCredFile    = "mail-credentials.enc"
	mailPendingFile = "mail-authorization.enc"
)

// ErrMailCredentialNotFound indicates that the selected Bot has not completed
// Agent Mail authorization.
var ErrMailCredentialNotFound = errors.New("mail credential not found")

// ErrMailCredentialAmbiguous means the current Bot token has Mail bindings in
// more than one Space and the caller did not select one.
var ErrMailCredentialAmbiguous = errors.New("multiple mail credentials match the active Bot")

// MailBinding identifies the local namespace selected for a mailbox token.
type MailBinding struct {
	Key     string
	RobotID string
	SpaceID string
}

// MailBindingKey derives the encrypted-store key for one Bot credential in one
// Space and API origin. Fingerprinting both the origin and Bot token prevents a
// credential authorized against one gateway or by one Bot token from being
// released to another. Neither raw value is persisted in the key.
func MailBindingKey(robotID, spaceID, botToken, apiOrigin string) (string, error) {
	if robotID == "" || spaceID == "" || botToken == "" || strings.TrimSpace(apiOrigin) == "" {
		return "", errors.New("robot id, space id, Bot token, and API origin are required")
	}
	normalizedOrigin, err := config.NormalizeAPIBaseURL(apiOrigin)
	if err != nil {
		return "", err
	}
	prefix := mailBindingPrefix("v2", robotID)
	space := base64.RawURLEncoding.EncodeToString([]byte(spaceID))
	originFingerprint := sha256.Sum256([]byte(normalizedOrigin))
	tokenFingerprint := sha256.Sum256([]byte(botToken))
	return fmt.Sprintf("%s%s:%x:%x", prefix, space, originFingerprint, tokenFingerprint), nil
}

func mailBindingPrefix(version, robotID string) string {
	return version + ":" + base64.RawURLEncoding.EncodeToString([]byte(robotID)) + ":"
}

func parseMailBindingKey(key string) (binding MailBinding, originFingerprint, tokenFingerprint string, ok bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 5 || parts[0] != "v2" || parts[1] == "" || parts[2] == "" || parts[3] == "" || parts[4] == "" {
		return MailBinding{}, "", "", false
	}
	robotID, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(robotID) == 0 {
		return MailBinding{}, "", "", false
	}
	spaceID, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(spaceID) == 0 {
		return MailBinding{}, "", "", false
	}
	return MailBinding{Key: key, RobotID: string(robotID), SpaceID: string(spaceID)}, parts[3], parts[4], true
}

func matchingMailBindings(keys []string, robotID, spaceID, botToken, apiOrigin string) ([]MailBinding, error) {
	if botToken == "" {
		return nil, errors.New("Bot token is required") //nolint:staticcheck // Bot is the product's canonical identity term.
	}
	normalizedOrigin, err := config.NormalizeAPIBaseURL(apiOrigin)
	if err != nil {
		return nil, err
	}
	originFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(normalizedOrigin)))
	tokenFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(botToken)))
	matches := make([]MailBinding, 0, 1)
	for _, key := range keys {
		binding, storedOriginFingerprint, storedTokenFingerprint, ok := parseMailBindingKey(key)
		if !ok || storedOriginFingerprint != originFingerprint || storedTokenFingerprint != tokenFingerprint {
			continue
		}
		if robotID != "" && binding.RobotID != robotID {
			continue
		}
		if spaceID != "" && binding.SpaceID != spaceID {
			continue
		}
		matches = append(matches, binding)
	}
	return matches, nil
}

func (s *Store) mailCredPath() string    { return filepath.Join(s.dir, mailCredFile) }
func (s *Store) mailPendingPath() string { return filepath.Join(s.dir, mailPendingFile) }

// PendingMailAuthorization is the local proof material for an in-progress
// device flow. DeviceCode and CodeVerifier are secrets and are only persisted
// in the encrypted credential directory.
type PendingMailAuthorization struct {
	DeviceCode      string `json:"device_code"`
	CodeVerifier    string `json:"code_verifier"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresAt       string `json:"expires_at"`
}

func (s *Store) loadMailTokens() (map[string]string, error) {
	blob, err := os.ReadFile(s.mailCredPath())
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", mailCredFile, err)
	}
	key, err := s.deriveKey()
	if err != nil {
		return nil, err
	}
	plain, err := open(blob, key)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt %s: %w", mailCredFile, err)
	}
	var tokens map[string]string
	if err := json.Unmarshal(plain, &tokens); err != nil {
		return nil, fmt.Errorf("parse decrypted %s: %w", mailCredFile, err)
	}
	if tokens == nil {
		tokens = map[string]string{}
	}
	return tokens, nil
}

func (s *Store) saveMailTokens(tokens map[string]string) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	if len(tokens) == 0 {
		if err := os.Remove(s.mailCredPath()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", mailCredFile, err)
		}
		return nil
	}
	plain, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", mailCredFile, err)
	}
	key, err := s.deriveKey()
	if err != nil {
		return err
	}
	blob, err := seal(plain, key)
	if err != nil {
		return err
	}
	return atomicWrite(s.mailCredPath(), blob, secPerm)
}

// SaveMailCredential stores the mailbox credential under its owning binding
// key. The secret uses a dedicated encrypted file so Bot and Mail tokens keep
// independent replacement and revocation lifecycles.
func (s *Store) SaveMailCredential(botKey, token string) error {
	if botKey == "" || token == "" {
		return errors.New("bot key and mail token are required")
	}
	tokens, err := s.loadMailTokens()
	if err != nil {
		return err
	}
	tokens[botKey] = token
	return s.saveMailTokens(tokens)
}

// GetMailCredential returns the mailbox credential bound to a Bot key.
func (s *Store) GetMailCredential(botKey string) (string, error) {
	tokens, err := s.loadMailTokens()
	if err != nil {
		return "", err
	}
	token, ok := tokens[botKey]
	if !ok {
		return "", fmt.Errorf("%w for Bot key %q", ErrMailCredentialNotFound, botKey)
	}
	return token, nil
}

// FindMailCredential selects a credential by the active Bot token, API origin,
// and optional RobotID/SpaceID claims. Token-only runtimes can recover the
// RobotID from the previously verified encrypted binding; multiple Space
// matches fail closed.
func (s *Store) FindMailCredential(robotID, spaceID, botToken, apiOrigin string) (string, MailBinding, error) {
	tokens, err := s.loadMailTokens()
	if err != nil {
		return "", MailBinding{}, err
	}
	keys := make([]string, 0, len(tokens))
	for key := range tokens {
		keys = append(keys, key)
	}
	matches, err := matchingMailBindings(keys, robotID, spaceID, botToken, apiOrigin)
	if err != nil {
		return "", MailBinding{}, err
	}
	switch len(matches) {
	case 0:
		return "", MailBinding{}, ErrMailCredentialNotFound
	case 1:
		return tokens[matches[0].Key], matches[0], nil
	default:
		return "", MailBinding{}, ErrMailCredentialAmbiguous
	}
}

// RemoveMailCredential revokes the local credential reference for a Bot
// key. Server-side revocation remains the authorization service's job.
func (s *Store) RemoveMailCredential(botKey string) error {
	tokens, err := s.loadMailTokens()
	if err != nil {
		return err
	}
	delete(tokens, botKey)
	return s.saveMailTokens(tokens)
}

func (s *Store) loadPendingMailAuthorizations() (map[string]PendingMailAuthorization, error) {
	blob, err := os.ReadFile(s.mailPendingPath())
	if os.IsNotExist(err) {
		return map[string]PendingMailAuthorization{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", mailPendingFile, err)
	}
	key, err := s.deriveKey()
	if err != nil {
		return nil, err
	}
	plain, err := open(blob, key)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt %s: %w", mailPendingFile, err)
	}
	var pending map[string]PendingMailAuthorization
	if err := json.Unmarshal(plain, &pending); err != nil {
		return nil, fmt.Errorf("parse decrypted %s: %w", mailPendingFile, err)
	}
	if pending == nil {
		pending = map[string]PendingMailAuthorization{}
	}
	return pending, nil
}

func (s *Store) savePendingMailAuthorizations(pending map[string]PendingMailAuthorization) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	if len(pending) == 0 {
		if err := os.Remove(s.mailPendingPath()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", mailPendingFile, err)
		}
		return nil
	}
	plain, err := json.Marshal(pending)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", mailPendingFile, err)
	}
	key, err := s.deriveKey()
	if err != nil {
		return err
	}
	blob, err := seal(plain, key)
	if err != nil {
		return err
	}
	return atomicWrite(s.mailPendingPath(), blob, secPerm)
}

// SavePendingMailAuthorization stores the encrypted proof material for the
// active Bot's in-progress device authorization.
func (s *Store) SavePendingMailAuthorization(botKey string, pending *PendingMailAuthorization) error {
	if botKey == "" || pending == nil || pending.DeviceCode == "" || pending.CodeVerifier == "" {
		return errors.New("bot key, device code, and code verifier are required")
	}
	items, err := s.loadPendingMailAuthorizations()
	if err != nil {
		return err
	}
	items[botKey] = *pending
	return s.savePendingMailAuthorizations(items)
}

// PendingMailAuthorization returns the in-progress authorization for a Bot key.
func (s *Store) PendingMailAuthorization(botKey string) (PendingMailAuthorization, error) {
	items, err := s.loadPendingMailAuthorizations()
	if err != nil {
		return PendingMailAuthorization{}, err
	}
	pending, ok := items[botKey]
	if !ok {
		return PendingMailAuthorization{}, fmt.Errorf("%w for Bot key %q", ErrMailCredentialNotFound, botKey)
	}
	return pending, nil
}

// FindPendingMailAuthorization is the pending-flow counterpart of
// FindMailCredential.
func (s *Store) FindPendingMailAuthorization(robotID, spaceID, botToken, apiOrigin string) (PendingMailAuthorization, MailBinding, error) {
	items, err := s.loadPendingMailAuthorizations()
	if err != nil {
		return PendingMailAuthorization{}, MailBinding{}, err
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	matches, err := matchingMailBindings(keys, robotID, spaceID, botToken, apiOrigin)
	if err != nil {
		return PendingMailAuthorization{}, MailBinding{}, err
	}
	switch len(matches) {
	case 0:
		return PendingMailAuthorization{}, MailBinding{}, ErrMailCredentialNotFound
	case 1:
		return items[matches[0].Key], matches[0], nil
	default:
		return PendingMailAuthorization{}, MailBinding{}, ErrMailCredentialAmbiguous
	}
}

// RemovePendingMailAuthorization deletes the in-progress authorization for a
// Bot key after it is completed or can no longer be used.
func (s *Store) RemovePendingMailAuthorization(botKey string) error {
	items, err := s.loadPendingMailAuthorizations()
	if err != nil {
		return err
	}
	delete(items, botKey)
	return s.savePendingMailAuthorizations(items)
}

func deleteMailBindingsForRobot(
	mailTokens map[string]string,
	pending map[string]PendingMailAuthorization,
	robotID string,
) {
	if robotID == "" {
		return
	}
	// Raw RobotID keys are removed as well so profile lifecycle operations also
	// clean up stores created by an earlier build of this feature branch.
	delete(mailTokens, robotID)
	delete(pending, robotID)
	prefixes := []string{mailBindingPrefix("v1", robotID), mailBindingPrefix("v2", robotID)}
	for key := range mailTokens {
		if strings.HasPrefix(key, prefixes[0]) || strings.HasPrefix(key, prefixes[1]) {
			delete(mailTokens, key)
		}
	}
	for key := range pending {
		if strings.HasPrefix(key, prefixes[0]) || strings.HasPrefix(key, prefixes[1]) {
			delete(pending, key)
		}
	}
}
