package icloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

var (
	// ErrNotLoggedIn means there is no stored session at all.
	ErrNotLoggedIn = errors.New("not signed in; run 'hme login'")
	// ErrSessionExpired means Apple examined the stored session and
	// refused it. Only a fresh sign-in helps.
	ErrSessionExpired = errors.New("the iCloud session expired or was revoked; run 'hme login'")
	// ErrAccountLocked is a sign-in refused because the account is
	// locked. Retrying the password only makes it worse.
	ErrAccountLocked = errors.New("this Apple Account is locked for security reasons; unlock it at https://iforgot.apple.com and then run 'hme login'")
)

// HTTPError is a non-2xx answer from an Apple endpoint. It names the
// host and path only: query strings carry account identifiers.
type HTTPError struct {
	Status   int
	Endpoint string
	Detail   string
}

func (e *HTTPError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("HTTP %d from %s", e.Status, e.Endpoint)
	}
	return fmt.Sprintf("HTTP %d from %s: %s", e.Status, e.Endpoint, e.Detail)
}

// APIError is Hide My Email answering a well-formed request with
// success=false.
type APIError struct {
	Op      string
	Code    string
	Message string
}

func (e *APIError) Error() string {
	switch {
	case e.Message != "" && e.Code != "":
		return fmt.Sprintf("%s failed: %s (code %s)", e.Op, e.Message, e.Code)
	case e.Message != "":
		return fmt.Sprintf("%s failed: %s", e.Op, e.Message)
	case e.Code != "":
		return fmt.Sprintf("%s failed (code %s)", e.Op, e.Code)
	}
	return e.Op + " failed"
}

// IsAuthError reports whether err means the user has to sign in again.
func IsAuthError(err error) bool {
	return errors.Is(err, ErrNotLoggedIn) || errors.Is(err, ErrSessionExpired)
}

// isRejection reports whether Apple definitively refused the session,
// as opposed to transport or server trouble that a retry may cure.
func isRejection(err error) bool {
	if errors.Is(err, ErrSessionExpired) {
		return true
	}
	// 421 is how iCloud answers missing or stale web cookies; 450 is
	// its "sign in again".
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == 401 || he.Status == 403 || he.Status == 421 || he.Status == 450
	}
	return false
}

// envelopeError is the error member of an HME response. Apple is loose
// about its shape: usually an object, but errorCode may be a number or
// a string, and sometimes the whole member is a bare scalar.
type envelopeError struct {
	Code    string
	Message string
}

func (e *envelopeError) UnmarshalJSON(data []byte) error {
	var obj struct {
		ErrorMessage string          `json:"errorMessage"`
		Reason       string          `json:"reason"`
		ErrorCode    json.RawMessage `json:"errorCode"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		e.Message = obj.ErrorMessage
		if e.Message == "" {
			e.Message = obj.Reason
		}
		e.Code = scalarText(obj.ErrorCode)
		return nil
	}
	e.Code = scalarText(data)
	return nil
}

func scalarText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// clean makes text from the network safe to print: one line, no control
// or formatting characters (which could drive a terminal), bounded.
func clean(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "..."
	}
	return s
}

// Sanitize makes text that came from the network safe to print on a
// terminal: a single line, no control or formatting characters, and at
// most limit runes.
func Sanitize(s string, limit int) string { return clean(s, limit) }
