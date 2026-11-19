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

	enc, err := s.Save(secret)
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
	if _, err := s.Save(secret); err != nil {
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
	_ = keyring.Delete(keyringService, keyringUser)
	if _, _, err := s.Load(); !errors.Is(err, ErrKeyMissing) {
		t.Errorf("missing key: err = %v", err)
	}
}

func TestFallsBackWithoutKeyring(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	s := New(t.TempDir(), false)
	enc, err := s.Save(secret)
	if err != nil || enc {
		t.Fatalf("Save: encrypted=%v err=%v", enc, err)
	}
	got, enc, err := s.Load()
	if err != nil || enc || !bytes.Equal(got, secret) {
		t.Fatalf("Load: %q encrypted=%v err=%v", got, enc, err)
	}
}

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no mode bits on Windows")
	}
	s := New(t.TempDir()+"/nested", true)
	if _, err := s.Save(secret); err != nil {
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
	if _, err := s.Save(secret); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Error("session file survived Delete")
	}
	if _, err := keyring.Get(keyringService, keyringUser); !errors.Is(err, keyring.ErrNotFound) {
		t.Error("key survived Delete")
	}
	if err := s.Delete(); err != nil {
		t.Errorf("second Delete: %v", err)
	}
}
