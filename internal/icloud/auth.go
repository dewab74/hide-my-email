package icloud

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dewab74/hide-my-email/internal/srp"
)

// TwoFactorRequest tells the caller where Apple sent the code.
type TwoFactorRequest struct {
	Method      string // "device" or "sms"
	Destination string // phone number for sms, "" for device
}

// CodeFunc asks the user for the six-digit verification code.
type CodeFunc func(ctx context.Context, req TwoFactorRequest) (string, error)

type accountInfo struct {
	DsInfo struct {
		Dsid             string `json:"dsid"`
		WebAccessAllowed *bool  `json:"isWebAccessAllowed"`
		HMEAvailable     *bool  `json:"isHideMyEmailFeatureAvailable"`
	} `json:"dsInfo"`
	Webservices map[string]struct {
		URL         string `json:"url"`
		PcsRequired bool   `json:"pcsRequired"`
	} `json:"webservices"`
	Success              *bool `json:"success"`
	HsaChallengeRequired *bool `json:"hsaChallengeRequired"`
}

func isFalse(b *bool) bool { return b != nil && !*b }

// Login signs in with the Apple ID and password, asking code for a
// two-factor code if Apple wants one. The password is used to derive
// an SRP proof and is never sent or stored. A trust token from an
// earlier sign-in on the same session lets Apple skip the second
// factor.
func (c *Client) Login(ctx context.Context, appleID string, password []byte, code CodeFunc) error {
	appleID = strings.ToLower(strings.TrimSpace(appleID))
	c.sess.AppleID = appleID
	c.frameID = newUUID()
	c.changed = true

	fid := "auth-" + c.frameID
	q := url.Values{
		"frame_id": {fid}, "iframeId": {fid}, "state": {fid},
		"language": {"en_US"}, "skVersion": {"7"}, "client_id": {widgetKey},
		"redirect_uri": {"https://www.icloud.com"}, "response_type": {"code"},
		"response_mode": {"web_message"}, "authVersion": {"latest"},
	}
	if resp, err := c.auth(ctx, "GET", "/authorize/signin?"+q.Encode(), nil); err != nil {
		return err
	} else if resp.status != 200 {
		return fmt.Errorf("starting sign-in: Apple answered HTTP %d", resp.status)
	}

	if resp, err := c.auth(ctx, "POST", "/federate?isRememberMeEnabled=true",
		map[string]any{"accountName": appleID, "rememberMe": true}); err != nil {
		return err
	} else if resp.status != 200 {
		return fmt.Errorf("starting sign-in: Apple answered HTTP %d", resp.status)
	}

	if err := c.srpSignIn(ctx, appleID, password, code); err != nil {
		return err
	}

	// Asking Apple to trust this installation is what lets later
	// sign-ins skip the code. After a second factor it must succeed. When
	// an existing trust token already got us in without one, a refusal
	// costs nothing: the session is good and the old token still stands.
	if resp, err := c.auth(ctx, "GET", "/2sv/trust", nil); err != nil {
		return err
	} else if resp.status != 200 && resp.status != 204 && c.didTwoFactor {
		return fmt.Errorf("trusting this session: Apple answered HTTP %d", resp.status)
	}
	if c.sess.SessionToken == "" {
		return errors.New("sign-in finished but Apple issued no session token")
	}

	if err := c.accountLogin(ctx); err != nil {
		// "Session expired, sign in again" would be absurd advice from
		// the sign-in command itself.
		if errors.Is(err, ErrSessionExpired) {
			return errors.New("sign-in succeeded, but iCloud refused the new session; try again in a few minutes")
		}
		return err
	}
	if c.account.HsaChallengeRequired != nil && *c.account.HsaChallengeRequired {
		return errors.New("two-factor authentication was not completed for this session; run 'hme login' again")
	}
	return nil
}

func (c *Client) srpSignIn(ctx context.Context, appleID string, password []byte, code CodeFunc) error {
	sc, err := srp.New()
	if err != nil {
		return err
	}
	resp, err := c.auth(ctx, "POST", "/signin/init", map[string]any{
		"a":           base64.StdEncoding.EncodeToString(sc.PublicKey()),
		"accountName": appleID,
		"protocols":   []string{"s2k", "s2k_fo"},
	})
	if err != nil {
		return err
	}
	if resp.status != 200 {
		return signinError("init", resp)
	}
	var init struct {
		Iteration int    `json:"iteration"`
		Salt      string `json:"salt"`
		Protocol  string `json:"protocol"`
		B         string `json:"b"`
		C         string `json:"c"`
	}
	if err := json.Unmarshal(resp.body, &init); err != nil {
		return errors.New("sign-in: could not read Apple's challenge")
	}
	salt, err1 := base64.StdEncoding.DecodeString(init.Salt)
	serverB, err2 := base64.StdEncoding.DecodeString(init.B)
	if err1 != nil || err2 != nil {
		return errors.New("sign-in: could not read Apple's challenge")
	}

	key, err := deriveKey(password, salt, init.Iteration, init.Protocol)
	if err != nil {
		return err
	}
	m1, m2, err := sc.Proofs([]byte(appleID), key, salt, serverB)
	clear(key)
	if err != nil {
		return fmt.Errorf("sign-in: %w", err)
	}

	trust := []string{}
	if c.sess.TrustToken != "" {
		trust = append(trust, c.sess.TrustToken)
	}
	resp, err = c.auth(ctx, "POST", "/signin/complete?isRememberMeEnabled=true", map[string]any{
		"accountName": appleID,
		"c":           init.C,
		"m1":          base64.StdEncoding.EncodeToString(m1),
		"m2":          base64.StdEncoding.EncodeToString(m2),
		"rememberMe":  true,
		"trustTokens": trust,
	})
	if err != nil {
		return err
	}
	switch resp.status {
	case 200:
		return nil
	case 409:
		return c.twoFactor(ctx, resp.body, code)
	case 412:
		return errors.New("this account needs attention before it can sign in here; sign in at https://account.apple.com and then retry")
	}
	return signinError("complete", resp)
}

// Bounds on what Apple may ask of the key derivation. Apple's real
// values sit well inside them (tens of thousands of iterations, a
// 16-byte salt).
const (
	minIterations = 1000
	maxIterations = 5_000_000
	minSaltBytes  = 8
)

// deriveKey turns the password into the SRP password key. Apple picks
// the protocol: s2k feeds the SHA-256 digest to PBKDF2, the older
// s2k_fo feeds its hex form.
func deriveKey(password, salt []byte, iterations int, protocol string) ([]byte, error) {
	// The count and salt come from the network. Bound them: too high a
	// count pins the CPU, and a trivial count or salt would make the
	// proof cheap to brute-force for anyone who could read it.
	if iterations < minIterations || iterations > maxIterations || len(salt) < minSaltBytes {
		return nil, errors.New("sign-in: Apple sent implausible key-derivation parameters")
	}
	// SHA-256 here is not the password hash: it is the input Apple's
	// protocol prescribes for PBKDF2 below, which is the slow, salted step.
	// Changing it would make every sign-in fail. (CodeQL flags this line as
	// weak password hashing; that alert is dismissed as a false positive.)
	sum := sha256.Sum256(password)
	defer clear(sum[:])
	in := sum[:]
	switch protocol {
	case "s2k":
	case "s2k_fo":
		in = []byte(hex.EncodeToString(sum[:]))
	default:
		return nil, fmt.Errorf("sign-in: unsupported password protocol %q", clean(protocol, 20))
	}
	return pbkdf2.Key(sha256.New, string(in), salt, iterations, 32)
}

type authOptions struct {
	TrustedPhoneNumbers     []trustedPhone `json:"trustedPhoneNumbers"`
	PhoneNumberVerification *struct {
		TrustedPhoneNumbers []trustedPhone `json:"trustedPhoneNumbers"`
	} `json:"phoneNumberVerification"`
	// NoTrustedDevices is Apple saying outright whether any device can
	// show a code. Absent on some accounts, hence the pointer.
	NoTrustedDevices *bool           `json:"noTrustedDevices"`
	FsaChallenge     json.RawMessage `json:"fsaChallenge"`
	FsaVerification  json.RawMessage `json:"fsaVerification"`
	KeyNames         []string        `json:"keyNames"`
}

type trustedPhone struct {
	ID                 int    `json:"id"`
	NumberWithDialCode string `json:"numberWithDialCode"`
	PushMode           string `json:"pushMode"`
}

func (o *authOptions) usesSecurityKey() bool {
	return len(o.FsaChallenge) > 0 || len(o.FsaVerification) > 0 || len(o.KeyNames) > 0
}

func (c *Client) twoFactor(ctx context.Context, signinBody []byte, code CodeFunc) error {
	resp, err := c.auth(ctx, "GET", "", nil)
	if err != nil {
		return err
	}
	var opts, fromSignin authOptions
	if err := json.Unmarshal(resp.body, &opts); err != nil {
		return errors.New("two-factor: could not read Apple's options")
	}
	_ = json.Unmarshal(signinBody, &fromSignin)
	if opts.usesSecurityKey() || fromSignin.usesSecurityKey() {
		return errors.New("this Apple Account signs in with a hardware security key, which hme does not support yet")
	}
	if code == nil {
		return errors.New("two-factor authentication is required")
	}

	phones := opts.TrustedPhoneNumbers
	if len(phones) == 0 && opts.PhoneNumberVerification != nil {
		phones = opts.PhoneNumberVerification.TrustedPhoneNumbers
	}
	// A text message is the fallback for accounts with no device to show
	// a code on. Apple's explicit statement decides when it makes one;
	// otherwise infer it from every phone being text-only.
	smsOnly := len(phones) > 0
	if opts.NoTrustedDevices != nil {
		smsOnly = smsOnly && *opts.NoTrustedDevices
	} else {
		for _, p := range phones {
			if p.PushMode != "sms" {
				smsOnly = false
			}
		}
	}

	path := "/verify/trusteddevice/securitycode"
	req := TwoFactorRequest{Method: "device"}
	body := map[string]any{}
	if smsOnly {
		phone := phones[0]
		path = "/verify/phone/securitycode"
		req = TwoFactorRequest{Method: "sms", Destination: clean(phone.NumberWithDialCode, 40)}
		body["phoneNumber"] = map[string]any{"id": phone.ID}
		body["mode"] = "sms"
		r, err := c.auth(ctx, "PUT", "/verify/phone", body)
		if err != nil {
			return err
		}
		if r.status != 200 {
			return fmt.Errorf("two-factor: Apple would not send a text message (HTTP %d)", r.status)
		}
	} else {
		// Newer accounts no longer push a code on their own; ask for
		// one. Best effort: older ones already did.
		_, _ = c.auth(ctx, "PUT", path, nil)
	}

	entered, err := code(ctx, req)
	if err != nil {
		return err
	}
	body["securityCode"] = map[string]string{"code": entered}
	resp, err = c.auth(ctx, "POST", path, body)
	if err != nil {
		return err
	}
	switch {
	case resp.status == 200 || resp.status == 204 || codeAccepted(resp):
		c.didTwoFactor = true
		return nil
	case resp.status == 400 || resp.status == 401 || resp.status == 403:
		return errors.New("incorrect verification code")
	}
	return fmt.Errorf("two-factor: Apple answered HTTP %d%s", resp.status, serviceErrorSuffix(resp.body))
}

// codeAccepted recognizes a valid code answered with 409, which Apple
// does for some accounts. Either tell suffices: a fresh session token
// (only issued on success) or securityCode.valid in the body. Trusting
// them is safe because the trust and accountLogin steps that follow
// refuse a session Apple did not verify.
func codeAccepted(resp *response) bool {
	if resp.status != 409 {
		return false
	}
	if resp.header.Get("X-Apple-Session-Token") != "" {
		return true
	}
	var verdict struct {
		SecurityCode struct {
			Valid bool `json:"valid"`
		} `json:"securityCode"`
	}
	return json.Unmarshal(resp.body, &verdict) == nil && verdict.SecurityCode.Valid
}

type serviceErrors struct {
	ServiceErrors []struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	} `json:"serviceErrors"`
}

func serviceErrorSuffix(body []byte) string {
	var se serviceErrors
	if json.Unmarshal(body, &se) == nil {
		for _, e := range se.ServiceErrors {
			if m := clean(e.Message, 200); m != "" {
				return ": " + m
			}
		}
	}
	return ""
}

func signinError(step string, resp *response) error {
	var se serviceErrors
	if json.Unmarshal(resp.body, &se) == nil {
		for _, e := range se.ServiceErrors {
			if scalarText(e.Code) == "-20209" {
				return ErrAccountLocked
			}
		}
	}
	detail := serviceErrorSuffix(resp.body)
	if resp.status == 401 || resp.status == 403 {
		// Only the complete step has checked the password.
		if step == "init" {
			return errors.New("Apple refused sign-in for this Apple ID" + detail)
		}
		return errors.New("incorrect Apple ID or password" + detail)
	}
	return fmt.Errorf("sign-in: Apple answered HTTP %d%s", resp.status, detail)
}

// auth calls Apple's identity service and absorbs the session headers
// it hands back.
func (c *Client) auth(ctx context.Context, method, path string, body any) (*response, error) {
	h := map[string]string{
		"Accept":                           "application/json",
		"Content-Type":                     "application/json",
		"X-Apple-OAuth-Client-Id":          widgetKey,
		"X-Apple-OAuth-Client-Type":        "firstPartyAuth",
		"X-Apple-OAuth-Redirect-URI":       "https://www.icloud.com",
		"X-Apple-OAuth-Require-Grant-Code": "true",
		"X-Apple-OAuth-Response-Mode":      "web_message",
		"X-Apple-OAuth-Response-Type":      "code",
		"X-Apple-Widget-Key":               widgetKey,
		"X-Requested-With":                 "XMLHttpRequest",
		"X-Apple-I-Require-UE":             "true",
		"X-Apple-Mandate-Security-Upgrade": "0",
		"X-Apple-Offer-Security-Upgrade":   "1",
		"Origin":                           "https://idmsa.apple.com",
		"Referer":                          "https://idmsa.apple.com/",
		"X-Apple-OAuth-State":              "auth-" + c.frameID,
		"X-Apple-Frame-Id":                 "auth-" + c.frameID,
		"X-Apple-I-FD-Client-Info":         `{"U":"` + userAgent + `","L":"en-US","Z":"GMT+00:00","V":"1.1","F":""}`,
	}
	if c.sess.Scnt != "" {
		h["scnt"] = c.sess.Scnt
	}
	if c.sess.SessionID != "" {
		h["X-Apple-ID-Session-Id"] = c.sess.SessionID
	}
	if c.authAttr != "" {
		h["X-Apple-Auth-Attributes"] = c.authAttr
	}

	resp, err := c.send(ctx, method, c.authBase+path, h, body)
	if err != nil {
		return nil, err
	}
	for header, dst := range map[string]*string{
		"scnt":                       &c.sess.Scnt,
		"X-Apple-ID-Session-Id":      &c.sess.SessionID,
		"X-Apple-Session-Token":      &c.sess.SessionToken,
		"X-Apple-TwoSV-Trust-Token":  &c.sess.TrustToken,
		"X-Apple-ID-Account-Country": &c.sess.AccountCountry,
		"X-Apple-Auth-Attributes":    &c.authAttr,
	} {
		if v := resp.header.Get(header); v != "" {
			*dst = v
		}
	}
	return resp, nil
}

// accountLogin trades the session and trust tokens for iCloud web
// cookies and the list of services. No password, no second factor.
func (c *Client) accountLogin(ctx context.Context) error {
	body, err := c.service(ctx, "POST", c.setupURL()+"/accountLogin", map[string]any{
		"accountCountryCode": c.sess.AccountCountry,
		"dsWebAuthToken":     c.sess.SessionToken,
		"extended_login":     true,
		"trustToken":         c.sess.TrustToken,
	})
	if err != nil {
		// A refusal here is Apple's verdict on the tokens themselves:
		// they expired or were revoked.
		if isRejection(err) {
			return fmt.Errorf("%w (%w)", ErrSessionExpired, err)
		}
		return fmt.Errorf("signing in to iCloud: %w", err)
	}
	if err := c.absorbAccount(body); err != nil {
		return err
	}
	c.fresh = true
	return nil
}

// validate asks Apple whether the stored cookies are still good.
func (c *Client) validate(ctx context.Context) error {
	body, err := c.service(ctx, "POST", c.setupURL()+"/validate", nil)
	if err != nil {
		return err
	}
	var probe accountInfo
	if json.Unmarshal(body, &probe) == nil && isFalse(probe.Success) && probe.DsInfo.Dsid == "" {
		return ErrSessionExpired
	}
	return c.absorbAccount(body)
}

func (c *Client) absorbAccount(body []byte) error {
	var info accountInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return errors.New("could not read iCloud's account response")
	}
	c.account = &info
	if info.DsInfo.Dsid != "" {
		c.sess.DSID = info.DsInfo.Dsid
	}
	c.sess.HMEBaseURL = ""
	if ws, ok := info.Webservices["premiummailsettings"]; ok && ws.URL != "" {
		if _, err := c.checkURL(ws.URL); err != nil {
			return err
		}
		c.sess.HMEBaseURL = strings.TrimRight(ws.URL, "/")
	}
	c.sess.ValidatedAt = time.Now()
	c.changed = true
	return nil
}

// Resume makes sure the stored session is usable, refreshing it
// quietly if Apple allows. It returns ErrNotLoggedIn or
// ErrSessionExpired when only a new sign-in will do.
func (c *Client) Resume(ctx context.Context) error {
	if c.sess.SessionToken == "" {
		return ErrNotLoggedIn
	}
	if c.sess.HMEBaseURL != "" && time.Since(c.sess.ValidatedAt) < validateTTL {
		return nil
	}
	return c.Refresh(ctx)
}

// Refresh checks the session with Apple regardless of when it was last
// confirmed.
func (c *Client) Refresh(ctx context.Context) error {
	if c.sess.SessionToken == "" {
		return ErrNotLoggedIn
	}
	err := c.validate(ctx)
	if err == nil {
		return nil
	}
	if !isRejection(err) {
		return fmt.Errorf("checking the iCloud session (possibly temporary, try again): %w", err)
	}
	return c.accountLogin(ctx)
}

// hmeUnavailable explains why the account lists no Hide My Email.
func (c *Client) hmeUnavailable() error {
	msg := "Hide My Email is not available for this Apple Account (it needs an iCloud+ subscription)"
	if c.account != nil && isFalse(c.account.DsInfo.WebAccessAllowed) {
		msg = `iCloud web access is turned off for this account; turn on "Access iCloud Data on the Web" ("Allow Data Access" on newer systems) in your device's iCloud settings and run 'hme login' again`
	}
	return errors.New(msg)
}
