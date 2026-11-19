// Package store persists the iCloud session on disk.
//
// The session is encrypted with AES-256-GCM under a random key that
// lives in the operating system's credential store (macOS Keychain,
// Windows Credential Manager, the Secret Service on Linux). The file
// alone, in a backup or a synced folder, is useless without that key.
// Where no credential store exists (a headless Linux box, say) the
// session falls back to a plain file readable only by its owner, and
// the caller is told so.
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "hme-cli"
	keyringUser    = "session-key"
	fileName       = "session.json"
	cipherGCM      = "aes-256-gcm"
	cipherNone     = "none"
)

// aad binds a ciphertext to its purpose and format version.
var aad = []byte("hme-cli session v1")

// ErrKeyMissing means the session file is encrypted but its key is no
// longer in the credential store.
var ErrKeyMissing = errors.New("the stored session cannot be decrypted (its key is missing from the system credential store); run 'hme login'")

var errMalformedKey = errors.New("malformed session key")

type envelope struct {
	Version int    `json:"version"`
	Cipher  string `json:"cipher"`
	Nonce   []byte `json:"nonce,omitempty"`
	Data    []byte `json:"data"`
}

// Store reads and writes one session file.
type Store struct {
	dir       string
	noKeyring bool
}

// Dir returns the configuration directory: $HME_CONFIG_DIR if set,
// otherwise the platform's per-user configuration directory.
func Dir() (string, error) {
	if d := os.Getenv("HME_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating the configuration directory: %w", err)
	}
	return filepath.Join(base, "hme"), nil
}

// New returns a store in dir. With noKeyring the credential store is
// never touched and the session is kept as a plain owner-only file.
func New(dir string, noKeyring bool) *Store {
	return &Store{dir: dir, noKeyring: noKeyring}
}

// Path is the session file's location.
func (s *Store) Path() string { return filepath.Join(s.dir, fileName) }

// Load returns the stored session, or nil if there is none. encrypted
// reports how it was stored.
func (s *Store) Load() (data []byte, encrypted bool, err error) {
	path := s.Path()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading session: %w", err)
	}
	// Refuse a session other users could have read or planted, the way
	// ssh refuses a loose private key. Windows has no mode bits; the
	// profile directory's ACL does this job there.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, false, fmt.Errorf("%s is accessible to other users (mode %04o); fix with 'chmod 600' or delete it and run 'hme login'", path, info.Mode().Perm())
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- the user's own config path
	if err != nil {
		return nil, false, fmt.Errorf("reading session: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Version != 1 {
		return nil, false, errors.New("the stored session is not in a format this version understands; run 'hme login'")
	}

	switch env.Cipher {
	case cipherNone:
		return env.Data, false, nil
	case cipherGCM:
		if s.noKeyring {
			return nil, false, ErrKeyMissing
		}
		key, err := loadKey()
		if err != nil {
			return nil, false, ErrKeyMissing
		}
		gcm, err := newGCM(key)
		if err != nil {
			return nil, false, err
		}
		if len(env.Nonce) != gcm.NonceSize() {
			return nil, false, ErrKeyMissing
		}
		plain, err := gcm.Open(nil, env.Nonce, env.Data, aad)
		if err != nil {
			// Wrong key or a tampered file; the same remedy either way.
			return nil, false, ErrKeyMissing
		}
		return plain, true, nil
	}
	return nil, false, errors.New("the stored session uses an unknown cipher; run 'hme login'")
}

// Save writes the session. encrypted is false when no credential store
// was available and the session went to a plain owner-only file.
func (s *Store) Save(data []byte) (encrypted bool, err error) {
	env := envelope{Version: 1, Cipher: cipherNone, Data: data}
	if !s.noKeyring {
		if key, kerr := loadOrCreateKey(); kerr == nil {
			gcm, err := newGCM(key)
			if err != nil {
				return false, err
			}
			nonce := make([]byte, gcm.NonceSize())
			if _, err := rand.Read(nonce); err != nil {
				return false, fmt.Errorf("generating nonce: %w", err)
			}
			env = envelope{Version: 1, Cipher: cipherGCM, Nonce: nonce, Data: gcm.Seal(nil, nonce, data, aad)}
			encrypted = true
		}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return false, fmt.Errorf("encoding session: %w", err)
	}
	return encrypted, writeAtomic(s.dir, s.Path(), raw)
}

// Delete removes the session file and its key.
func (s *Store) Delete() error {
	if !s.noKeyring {
		// Best effort: the key is worthless once the file is gone.
		_ = keyring.Delete(keyringService, keyringUser)
	}
	if err := os.Remove(s.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing session: %w", err)
	}
	return nil
}

// writeAtomic writes via a temporary file and a rename, so a crash
// never leaves a half-written session. The temporary file is created
// owner-only, so the secret is never on disk with wider permissions.
func writeAtomic(dir, path string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".session-*.tmp") // mode 0600
	if err != nil {
		return fmt.Errorf("writing session: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing session: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing session: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing session: %w", err)
	}
	return nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("preparing cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func loadKey() ([]byte, error) {
	enc, err := keyring.Get(keyringService, keyringUser)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(key) != 32 {
		return nil, errMalformedKey
	}
	return key, nil
}

func loadOrCreateKey() ([]byte, error) {
	key, err := loadKey()
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) && !errors.Is(err, errMalformedKey) {
		return nil, err // no usable credential store
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := keyring.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(key)); err != nil {
		return nil, err
	}
	return key, nil
}
