package authstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const saltLen = 32

// loadTokens decrypts and parses credentials.enc into a profile→token map. A
// missing file yields an empty (non-nil) map.
func (s *Store) loadTokens() (map[string]string, error) {
	blob, err := os.ReadFile(s.credPath())
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", credFile, err)
	}
	key, err := s.deriveKey()
	if err != nil {
		return nil, err
	}
	plain, err := open(blob, key)
	if err != nil {
		return nil, fmt.Errorf(
			"cannot decrypt %s — the encryption key changed (machine id changed via host re-image / VM migration / restored backup) or the file is corrupt; remove %s and run `octo auth login` to re-store: %w",
			credFile, s.credPath(), err)
	}
	var tokens map[string]string
	if err := json.Unmarshal(plain, &tokens); err != nil {
		return nil, fmt.Errorf("parse decrypted credentials: %w", err)
	}
	if tokens == nil {
		tokens = map[string]string{}
	}
	return tokens, nil
}

// saveTokens encrypts the profile→token map to credentials.enc (0600). When the
// map is empty the file is removed so the directory stays clean.
func (s *Store) saveTokens(tokens map[string]string) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	if len(tokens) == 0 {
		if err := os.Remove(s.credPath()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", credFile, err)
		}
		return nil
	}
	plain, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	key, err := s.deriveKey()
	if err != nil {
		return err
	}
	blob, err := seal(plain, key)
	if err != nil {
		return err
	}
	return atomicWrite(s.credPath(), blob, secPerm)
}

// deriveKey returns the AES-256 key for this store: SHA256(machineID || salt).
// Binding to the machine id means a copied/synced credentials.enc cannot be
// decrypted elsewhere; the random salt adds entropy and is created on demand. A
// machine id that can't be read degrades to salt-only material (still usable on
// this machine, just without the off-machine binding).
func (s *Store) deriveKey() ([32]byte, error) {
	salt, err := s.getOrCreateSalt()
	if err != nil {
		return [32]byte{}, err
	}
	// A platform with no machine id derives a stable salt-only key. But a
	// transient read failure on a platform that HAS one must surface — folding
	// an empty id into the key would silently change it and make every stored
	// token undecryptable for that run.
	id, err := machineID()
	if err != nil && !errors.Is(err, errMachineIDUnsupported) {
		return [32]byte{}, fmt.Errorf("read machine id: %w", err)
	}
	h := sha256.New()
	h.Write([]byte(id))
	h.Write(salt)
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key, nil
}

func (s *Store) getOrCreateSalt() ([]byte, error) {
	salt, err := os.ReadFile(s.saltPath())
	if err == nil {
		if len(salt) == saltLen {
			return salt, nil
		}
		// A wrong-length salt must not be silently regenerated: a new salt
		// changes the key and makes every stored token undecryptable. Fail loud.
		return nil, fmt.Errorf(
			"%s is corrupt: expected %d bytes, got %d — remove %s and run `octo auth login` to re-store",
			saltFile, saltLen, len(salt), s.saltPath())
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", saltFile, err)
	}
	if err := s.ensureDir(); err != nil {
		return nil, err
	}
	salt = make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	if err := atomicWrite(s.saltPath(), salt, secPerm); err != nil {
		return nil, err
	}
	return salt, nil
}

// seal encrypts plaintext with AES-256-GCM, prefixing the random nonce.
func seal(plaintext []byte, key [32]byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// open reverses seal: it splits the nonce prefix and decrypts.
func open(blob []byte, key [32]byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(blob) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	return gcm.Open(nil, nonce, ct, nil)
}

func newGCM(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return gcm, nil
}
