package store

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"testing"

	"github.com/zalando/go-keyring"
)

var secret = []byte(`{"sessionToken":"very-secret-token"}`)

func TestEncryptedRoundTrip(t *testing.T) {
	keyring.MockInit()
	s := New(t.TempDir(), false)

	enc, err := s.Save(secret, true)
	if err != nil || !enc {
		t.Fatalf("Save: encrypted=%v err=%v", enc, err)
	}
	raw, _ := os.ReadFile(s.Path())
	if bytes.Contains(raw, []byte("very-secret-token")) {
		t.Fatal("session file contains the plaintext")
	}
	got, enc, err := s.Load()
	if err != nil || !enc || !bytes.Equal(got, secret) {
		t.Fatalf("Load: %q encrypted=%v err=%v", got, enc, err)
	}
}

func TestTamperedOrKeylessFileIsRejected(t *testing.T) {
	keyring.MockInit()
	s := New(t.TempDir(), false)
	if _, err := s.Save(secret, true); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(s.Path())
	tampered := bytes.Replace(raw, []byte(`"data":"`), []byte(`"data":"AAAA`), 1)
	if err := os.WriteFile(s.Path(), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load(); !errors.Is(err, ErrKeyMissing) {
		t.Errorf("tampered file: err = %v", err)
	}

	if err := os.WriteFile(s.Path(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = keyring.Delete(keyringService, s.keyUser)
	if _, _, err := s.Load(); !errors.Is(err, ErrKeyMissing) {
		t.Errorf("missing key: err = %v", err)
	}
}

func TestFallsBackWithoutKeyring(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	s := New(t.TempDir(), false)
	enc, err := s.Save(secret, true)
	if err != nil || enc {
		t.Fatalf("Save: encrypted=%v err=%v", enc, err)
	}
	got, enc, err := s.Load()
	if err != nil || enc || !bytes.Equal(got, secret) {
		t.Fatalf("Load: %q encrypted=%v err=%v", got, enc, err)
	}
}

func TestPlaintextDowngradeIsRejected(t *testing.T) {
	keyring.MockInit()
	s := New(t.TempDir(), false)
	if _, err := s.Save(secret, false); err != nil {
		t.Fatal(err)
	}
	// Someone who can write the file swaps in a plain one of their own.
	planted := `{"version":1,"cipher":"none","data":"e30="}`
	if err := os.WriteFile(s.Path(), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _, err := s.Load(); !errors.Is(err, ErrDowngraded) || got != nil {
		t.Errorf("planted plaintext session: %q, %v", got, err)
	}
}

func TestEncryptedSessionIsNeverResavedInTheClear(t *testing.T) {
	keyring.MockInit()
	s := New(t.TempDir(), false)
	if _, err := s.Save(secret, false); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.Path())

	// The credential store goes away between load and save.
	keyring.MockInitWithError(errors.New("keyring locked"))
	if _, err := s.Save([]byte("newer session"), false); !errors.Is(err, ErrNoKeyring) {
		t.Errorf("Save without a keyring: err = %v", err)
	}
	if after, _ := os.ReadFile(s.Path()); !bytes.Equal(before, after) {
		t.Error("the session file was rewritten")
	}
	// HME_NO_KEYRING on a session that must stay encrypted: same answer.
	if _, err := New(s.dir, true).Save(secret, false); !errors.Is(err, ErrNoKeyring) {
		t.Errorf("no-keyring Save with allowPlaintext=false: err = %v", err)
	}
}

func TestSharedDirectoryIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no mode bits on Windows")
	}
	s := New(t.TempDir(), true)
	if _, err := s.Save(secret, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.dir, 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	if _, _, err := s.Load(); err == nil {
		t.Error("a session in a world-writable directory was accepted")
	}
}

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no mode bits on Windows")
	}
	s := New(t.TempDir()+"/nested", true)
	if _, err := s.Save(secret, true); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(s.Path()); info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %04o, want 0600", info.Mode().Perm())
	}
	if info, _ := os.Stat(s.dir); info.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %04o, want 0700", info.Mode().Perm())
	}

	if err := os.Chmod(s.Path(), 0o644); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	if _, _, err := s.Load(); err == nil {
		t.Error("a world-readable session file was accepted")
	}
}

func TestLoadMissingAndDelete(t *testing.T) {
	keyring.MockInit()
	s := New(t.TempDir(), false)
	if got, _, err := s.Load(); got != nil || err != nil {
		t.Fatalf("Load on empty dir: %q %v", got, err)
	}
	if _, err := s.Save(secret, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Error("session file survived Delete")
	}
	if _, err := keyring.Get(keyringService, s.keyUser); !errors.Is(err, keyring.ErrNotFound) {
		t.Error("key survived Delete")
	}
	if err := s.Delete(); err != nil {
		t.Errorf("second Delete: %v", err)
	}
}

func TestEachDirectoryHasItsOwnKey(t *testing.T) {
	keyring.MockInit()
	a, b := New(t.TempDir(), false), New(t.TempDir(), false)
	if a.keyUser == b.keyUser || a.keyUser == keyringUser {
		t.Fatalf("key names: %q, %q", a.keyUser, b.keyUser)
	}
	for _, s := range []*Store{a, b} {
		if _, err := s.Save(secret, false); err != nil {
			t.Fatal(err)
		}
	}
	// Signing out of one leaves the other readable.
	if err := a.Delete(); err != nil {
		t.Fatal(err)
	}
	if got, enc, err := b.Load(); err != nil || !enc || !bytes.Equal(got, secret) {
		t.Errorf("the other directory's session: %q %v %v", got, enc, err)
	}
	// The default location keeps the original, unsuffixed name.
	if base, err := os.UserConfigDir(); err == nil {
		if got := keyUserFor(base + "/hme"); got != keyringUser {
			t.Errorf("default directory key = %q", got)
		}
	}
}

func TestUnusableFilesAreReportedAsSuch(t *testing.T) {
	keyring.MockInit()
	for _, content := range []string{"not json", `{"version":2,"cipher":"none","data":""}`, `{"version":1,"cipher":"rot13","data":""}`} {
		s := New(t.TempDir(), false)
		if err := os.WriteFile(s.Path(), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Load(); !errors.Is(err, ErrUnusable) {
			t.Errorf("%q: err = %v", content, err)
		}
	}
}
