package authstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testMailAPIOrigin = "https://octo.example"

func TestMailCredentialLifecycleUsesDedicatedEncryptedFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvConfigDir, dir)
	store, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := store.SaveMailCredential("bot-a", "omb_secret_value"); err != nil {
		t.Fatalf("SaveMailCredential: %v", err)
	}
	got, err := store.GetMailCredential("bot-a")
	if err != nil || got != "omb_secret_value" {
		t.Fatalf("GetMailCredential = %q, %v", got, err)
	}

	blob, err := os.ReadFile(filepath.Join(dir, mailCredFile))
	if err != nil {
		t.Fatalf("read encrypted file: %v", err)
	}
	if strings.Contains(string(blob), "omb_secret_value") {
		t.Fatal("mail credential was stored in plaintext")
	}
	if _, err := os.Stat(filepath.Join(dir, credFile)); !os.IsNotExist(err) {
		t.Fatalf("Bot credential file should remain separate, stat error = %v", err)
	}

	if err := store.RemoveMailCredential("bot-a"); err != nil {
		t.Fatalf("RemoveMailCredential: %v", err)
	}
	if _, err := store.GetMailCredential("bot-a"); !errors.Is(err, ErrMailCredentialNotFound) {
		t.Fatalf("GetMailCredential after remove = %v", err)
	}
}

func TestMailBindingKeyScopesRobotSpaceAndBotCredential(t *testing.T) {
	keyA, err := MailBindingKey("bot-a", "space-a", "app_bot_a", testMailAPIOrigin)
	if err != nil {
		t.Fatalf("MailBindingKey: %v", err)
	}
	keyB, err := MailBindingKey("bot-a", "space-b", "app_bot_a", testMailAPIOrigin)
	if err != nil {
		t.Fatalf("MailBindingKey second Space: %v", err)
	}
	keyRotated, err := MailBindingKey("bot-a", "space-a", "app_bot_a_rotated", testMailAPIOrigin)
	if err != nil {
		t.Fatalf("MailBindingKey rotated token: %v", err)
	}
	keyOtherOrigin, err := MailBindingKey("bot-a", "space-a", "app_bot_a", "https://other.example")
	if err != nil {
		t.Fatalf("MailBindingKey other origin: %v", err)
	}
	if keyA == keyB || keyA == keyRotated || keyA == keyOtherOrigin || keyB == keyRotated {
		t.Fatalf("binding keys are not isolated: %q %q %q %q", keyA, keyB, keyRotated, keyOtherOrigin)
	}
	if strings.Contains(keyA, "app_bot_a") {
		t.Fatalf("binding key exposes Bot token: %q", keyA)
	}
	if strings.Contains(keyA, testMailAPIOrigin) {
		t.Fatalf("binding key exposes API origin: %q", keyA)
	}
	keyWithSlash, err := MailBindingKey("bot-a", "space-a", "app_bot_a", testMailAPIOrigin+"/")
	if err != nil || keyWithSlash != keyA {
		t.Fatalf("equivalent origins produced different keys: %q, %q, %v", keyA, keyWithSlash, err)
	}
	for _, input := range [][4]string{
		{"", "space-a", "app_a", testMailAPIOrigin},
		{"bot-a", "", "app_a", testMailAPIOrigin},
		{"bot-a", "space-a", "", testMailAPIOrigin},
		{"bot-a", "space-a", "app_a", ""},
	} {
		if _, err := MailBindingKey(input[0], input[1], input[2], input[3]); err == nil {
			t.Fatalf("MailBindingKey%q accepted an empty component", input)
		}
	}
}

func TestFindMailCredentialUsesTokenBindingAndFailsClosedOnSpaceAmbiguity(t *testing.T) {
	t.Setenv(EnvConfigDir, t.TempDir())
	store, err := New()
	if err != nil {
		t.Fatal(err)
	}
	keyA, _ := MailBindingKey("bot-a", "space-a", "app_a", testMailAPIOrigin)
	keyB, _ := MailBindingKey("bot-a", "space-b", "app_a", testMailAPIOrigin)
	if err := store.SaveMailCredential(keyA, "omb_a"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMailCredential(keyB, "omb_b"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.FindMailCredential("bot-a", "", "app_a", testMailAPIOrigin); !errors.Is(err, ErrMailCredentialAmbiguous) {
		t.Fatalf("FindMailCredential without Space = %v", err)
	}
	token, binding, err := store.FindMailCredential("", "space-b", "app_a", testMailAPIOrigin)
	if err != nil || token != "omb_b" || binding.RobotID != "bot-a" || binding.SpaceID != "space-b" {
		t.Fatalf("FindMailCredential selected %q, %+v, %v", token, binding, err)
	}
	if _, _, err := store.FindMailCredential("bot-a", "space-a", "app_rotated", testMailAPIOrigin); !errors.Is(err, ErrMailCredentialNotFound) {
		t.Fatalf("rotated Bot token unlocked old Mail credential: %v", err)
	}
	if _, _, err := store.FindMailCredential("bot-a", "space-a", "app_a", "https://other.example"); !errors.Is(err, ErrMailCredentialNotFound) {
		t.Fatalf("different API origin unlocked old Mail credential: %v", err)
	}
}

func TestFindPendingMailAuthorizationRequiresMatchingAPIOrigin(t *testing.T) {
	t.Setenv(EnvConfigDir, t.TempDir())
	store, err := New()
	if err != nil {
		t.Fatal(err)
	}
	key, err := MailBindingKey("bot-a", "space-a", "app_a", testMailAPIOrigin)
	if err != nil {
		t.Fatal(err)
	}
	pending := &PendingMailAuthorization{
		DeviceCode: "omd_secret", CodeVerifier: "verifier_secret",
	}
	if err := store.SavePendingMailAuthorization(key, pending); err != nil {
		t.Fatal(err)
	}

	got, binding, err := store.FindPendingMailAuthorization(
		"bot-a", "space-a", "app_a", testMailAPIOrigin+"/",
	)
	if err != nil || got.DeviceCode != pending.DeviceCode || binding.Key != key {
		t.Fatalf("matching origin selected %+v, %+v, %v", got, binding, err)
	}
	if _, _, err := store.FindPendingMailAuthorization(
		"bot-a", "space-a", "app_a", "https://other.example",
	); !errors.Is(err, ErrMailCredentialNotFound) {
		t.Fatalf("different API origin unlocked pending authorization: %v", err)
	}
}

func TestPendingMailAuthorizationIsEncrypted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvConfigDir, dir)
	store, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pending := PendingMailAuthorization{
		DeviceCode: "omd_device_secret", CodeVerifier: "verifier_secret",
		UserCode: "ABCD-EFGH", VerificationURI: "https://octo.example/mail/authorize?code=ABCD-EFGH",
		ExpiresAt: "2026-07-24T12:00:00Z",
	}
	if err := store.SavePendingMailAuthorization("agent", &pending); err != nil {
		t.Fatalf("SavePendingMailAuthorization: %v", err)
	}
	got, err := store.PendingMailAuthorization("agent")
	if err != nil || got.DeviceCode != pending.DeviceCode || got.CodeVerifier != pending.CodeVerifier {
		t.Fatalf("PendingMailAuthorization = %+v, %v", got, err)
	}
	blob, err := os.ReadFile(filepath.Join(dir, mailPendingFile))
	if err != nil {
		t.Fatalf("read encrypted pending file: %v", err)
	}
	if strings.Contains(string(blob), pending.DeviceCode) || strings.Contains(string(blob), pending.CodeVerifier) {
		t.Fatal("pending device authorization was stored in plaintext")
	}
	if err := store.RemovePendingMailAuthorization("agent"); err != nil {
		t.Fatalf("RemovePendingMailAuthorization: %v", err)
	}
}

func TestMailCredentialFollowsBotProfileIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvConfigDir, dir)
	store, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := store.SaveProfile("agent", &ProfileMeta{RobotID: "bot-a", SpaceID: "space-a"}, "app_a"); err != nil {
		t.Fatalf("SaveProfile bot-a: %v", err)
	}
	keyA, err := MailBindingKey("bot-a", "space-a", "app_a", testMailAPIOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMailCredential(keyA, "omb_a"); err != nil {
		t.Fatalf("SaveMailCredential: %v", err)
	}

	// Replacing the profile token invalidates the prior mailbox binding even
	// when the locally claimed RobotID stays unchanged.
	if err := store.SaveProfile("agent", &ProfileMeta{RobotID: "bot-a", SpaceID: "space-a"}, "app_a2"); err != nil {
		t.Fatalf("refresh bot-a: %v", err)
	}
	if _, err := store.GetMailCredential(keyA); !errors.Is(err, ErrMailCredentialNotFound) {
		t.Fatalf("mail credential survived Bot token replacement: %v", err)
	}

	// Reusing the friendly profile name for another Bot revokes local mailbox
	// access rather than silently transferring the old binding.
	if err := store.SaveProfile("agent", &ProfileMeta{RobotID: "bot-b", SpaceID: "space-b"}, "app_b"); err != nil {
		t.Fatalf("replace with bot-b: %v", err)
	}
	if _, err := store.GetMailCredential(keyA); !errors.Is(err, ErrMailCredentialNotFound) {
		t.Fatalf("mail credential transferred across Bots: %v", err)
	}

	keyB, err := MailBindingKey("bot-b", "space-b", "app_b", testMailAPIOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMailCredential(keyB, "omb_b"); err != nil {
		t.Fatalf("SaveMailCredential bot-b: %v", err)
	}
	if err := store.RemoveProfile("agent"); err != nil {
		t.Fatalf("RemoveProfile: %v", err)
	}
	if _, err := store.GetMailCredential(keyB); !errors.Is(err, ErrMailCredentialNotFound) {
		t.Fatalf("mail credential survived profile removal: %v", err)
	}
}

func TestSavingDefaultNamedProfilePreservesExistingRobotIDMailCredential(t *testing.T) {
	t.Setenv(EnvConfigDir, t.TempDir())
	store, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := store.SaveMailCredential("bot-a", "omb_a"); err != nil {
		t.Fatalf("SaveMailCredential: %v", err)
	}
	// `auth login --bot-id bot-a` defaults the profile name to the RobotID.
	// Creating that stored profile must not erase a Mail credential that the
	// same Bot already obtained while running from the environment.
	if err := store.SaveProfile("bot-a", &ProfileMeta{RobotID: "bot-a"}, "app_a"); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if got, err := store.GetMailCredential("bot-a"); err != nil || got != "omb_a" {
		t.Fatalf("mail credential after profile creation = %q, %v", got, err)
	}
}

func TestChangingProfileSpaceRemovesOldMailBinding(t *testing.T) {
	t.Setenv(EnvConfigDir, t.TempDir())
	store, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProfile("agent", &ProfileMeta{RobotID: "bot-a", SpaceID: "space-a"}, "app_a"); err != nil {
		t.Fatal(err)
	}
	key, err := MailBindingKey("bot-a", "space-a", "app_a", testMailAPIOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMailCredential(key, "omb_a"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProfile("agent", &ProfileMeta{RobotID: "bot-a", SpaceID: "space-b"}, "app_a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMailCredential(key); !errors.Is(err, ErrMailCredentialNotFound) {
		t.Fatalf("mail credential survived Space rebind: %v", err)
	}
}
