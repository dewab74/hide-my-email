// Package icloudtest is a fake Apple for tests: the identity service's
// SRP and two-factor sign-in, the iCloud setup service, and the Hide My
// Email API, all on one local HTTP server. It checks SRP proofs and
// cookies for real, so a client that passes against it has done the
// protocol and not merely made the calls.
package icloudtest

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dewab74/hide-my-email/internal/srp"
)

// Credentials the fake accepts. Fixtures, not real secrets.
const (
	AppleID  = "tester@example.com"
	Password = "correct horse battery staple" // pragma: allowlist secret
	Code     = "123456"
	Phone    = "+1 (•••) •••-••42"
)

// Alias is an address as the fake stores it.
type Alias struct {
	ID        string `json:"anonymousId"`
	Address   string `json:"hme"`
	Label     string `json:"label"`
	Note      string `json:"note"`
	Active    bool   `json:"isActive"`
	ForwardTo string `json:"forwardToEmail"`
	Created   int64  `json:"createTimestamp"`
}

// Fake is the server and its knobs. Set knobs before the request they
// should affect; read state through the methods.
type Fake struct {
	*httptest.Server

	// Protocol is the password protocol offered: "s2k" or "s2k_fo".
	Protocol string
	// SMSOnly makes the account's second factor a text message.
	SMSOnly bool
	// SecurityKey makes the account require a hardware key.
	SecurityKey bool
	// NoHME removes Hide My Email from the account's services.
	NoHME bool
	// HMEURL, if set, is advertised as the Hide My Email service URL.
	HMEURL string
	// NoTrustedDevices, if set, is stated in the two-factor options, as
	// Apple does for most accounts.
	NoTrustedDevices *bool
	// RejectAccountLogin makes the setup service refuse every session.
	RejectAccountLogin bool
	// RefuseTrust makes the trust endpoint answer with an error.
	RefuseTrust bool
	// FailDeleteOf makes deleting the alias with this ID fail.
	FailDeleteOf string
	// OmitForwardList leaves the inbox list out of the listing.
	OmitForwardList bool
	// NewAddress, if set, names each generated address. The default is
	// fresh-alias-N@icloud.com.
	NewAddress func() string
	// Now, if set, timestamps new addresses. The default is a fixed time.
	Now func() time.Time

	mu           sync.Mutex
	srp          *srp.Server
	sessionToken string
	trustToken   string
	webCookie    string
	serial       int
	aliases      []Alias
	forwardTo    string
	requests     []string
	codePrompts  int
}

// New starts a fake with a few addresses already on the account.
func New(t *testing.T) *Fake {
	t.Helper()
	f := newFake()
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// Listen starts a fake on addr (such as "127.0.0.1:8765") that outlives a
// test, for demonstrations. The caller closes it.
func Listen(addr string) (*Fake, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	f := newFake()
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	_ = f.Listener.Close()
	f.Listener = l
	f.Start()
	return f, nil
}

func newFake() *Fake {
	f := &Fake{Protocol: "s2k", forwardTo: "tester@icloud.com"}
	f.aliases = []Alias{
		{ID: "id-1", Address: "old-shop.0a@icloud.com", Label: "Old shop", Note: "closed", Active: false, ForwardTo: f.forwardTo, Created: 1_700_000_000_000},
		{ID: "id-2", Address: "news-letter.1b@icloud.com", Label: "Newsletter", Active: true, ForwardTo: f.forwardTo, Created: 1_710_000_000_000},
	}
	return f
}

// SetAliases replaces the addresses on the account.
func (f *Fake) SetAliases(as []Alias) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aliases = append([]Alias(nil), as...)
}

// AuthBase and SetupBase are the endpoints to hand the client.
func (f *Fake) AuthBase() string  { return f.URL + "/auth" }
func (f *Fake) SetupBase() string { return f.URL + "/setup" }

// Aliases returns the account's current addresses.
func (f *Fake) Aliases() []Alias {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Alias(nil), f.aliases...)
}

// Requests returns "METHOD /path" for every request so far.
func (f *Fake) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// CodePrompts is how many times a verification code was sent out.
func (f *Fake) CodePrompts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.codePrompts
}

// ExpireCookies invalidates the web cookies, as iCloud does after a
// while. The session token still works, so a client can recover.
func (f *Fake) ExpireCookies() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.webCookie = ""
}

// RevokeSession invalidates everything; only a new sign-in helps.
func (f *Fake) RevokeSession() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.webCookie, f.sessionToken, f.trustToken = "", "", ""
}

func (f *Fake) next(prefix string) string {
	f.serial++
	return fmt.Sprintf("%s-%d", prefix, f.serial)
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func serviceError(code, msg string) map[string]any {
	return map[string]any{"serviceErrors": []map[string]string{{"code": code, "message": msg}}}
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)

	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	str := func(k string) string { s, _ := body[k].(string); return s }

	switch p := r.URL.Path; {
	case strings.HasPrefix(p, "/auth"):
		f.serveAuth(w, r, strings.TrimPrefix(p, "/auth"), body, str)
	case p == "/setup/accountLogin":
		if f.RejectAccountLogin || f.sessionToken == "" || str("dsWebAuthToken") != f.sessionToken {
			reply(w, 421, map[string]any{"success": false, "error": "Invalid authentication token"})
			return
		}
		f.webCookie = f.next("webauth")
		// Quoted, like Apple's real cookie values.
		w.Header().Add("Set-Cookie", `X-APPLE-WEBAUTH-TOKEN="v=2:t=`+f.webCookie+`"; Path=/; Secure; HttpOnly`)
		reply(w, 200, f.account())
	case p == "/setup/validate":
		if !f.cookieOK(r) {
			reply(w, 421, map[string]any{"success": false, "error": "Missing X-APPLE-WEBAUTH-TOKEN cookie"})
			return
		}
		reply(w, 200, f.account())
	case strings.HasPrefix(p, "/hme/"):
		if !f.cookieOK(r) {
			reply(w, 401, map[string]any{"reason": "Invalid global session"})
			return
		}
		if r.URL.Query().Get("dsid") != "1234567890" || r.URL.Query().Get("clientId") == "" {
			reply(w, 400, map[string]any{"reason": "missing dsid or clientId"})
			return
		}
		f.serveHME(w, strings.TrimPrefix(p, "/hme/"), str)
	default:
		reply(w, 404, nil)
	}
}

func (f *Fake) cookieOK(r *http.Request) bool {
	ck, err := r.Cookie("X-APPLE-WEBAUTH-TOKEN")
	return err == nil && f.webCookie != "" && ck.Value == "v=2:t="+f.webCookie
}

func (f *Fake) account() map[string]any {
	services := map[string]any{"ckdatabasews": map[string]any{"url": f.URL + "/ck"}}
	if !f.NoHME {
		u := f.URL + "/hme"
		if f.HMEURL != "" {
			u = f.HMEURL
		}
		services["premiummailsettings"] = map[string]any{"url": u, "status": "active"}
	}
	return map[string]any{
		"dsInfo":               map[string]any{"dsid": "1234567890", "appleId": AppleID, "isWebAccessAllowed": true},
		"webservices":          services,
		"hsaChallengeRequired": false,
	}
}

// passKey derives the password key the way Apple's server does, so the
// fake can check proofs: SHA-256 of the fixture password, then PBKDF2.
// Test code; the hashing scheme is Apple's, not a choice.
func passKey(protocol string, salt []byte, iterations int) []byte {
	sum := sha256.Sum256([]byte(Password))
	in := string(sum[:])
	if protocol == "s2k_fo" {
		in = hex.EncodeToString(sum[:])
	}
	key, _ := pbkdf2.Key(sha256.New, in, salt, iterations, 32)
	return key
}

func (f *Fake) serveAuth(w http.ResponseWriter, r *http.Request, path string, body map[string]any, str func(string) string) {
	const iterations = 1000
	salt := []byte("fake-apple-salt!")
	w.Header().Set("scnt", "scnt-value")
	w.Header().Set("X-Apple-ID-Session-Id", "idms-session")

	codeBody := func() string {
		sc, _ := body["securityCode"].(map[string]any)
		s, _ := sc["code"].(string)
		return s
	}
	checkCode := func() {
		if codeBody() != Code {
			reply(w, 400, serviceError("-21669", "Incorrect verification code."))
			return
		}
		f.sessionToken = f.next("session")
		w.Header().Set("X-Apple-Session-Token", f.sessionToken)
		reply(w, 204, nil)
	}

	switch r.Method + " " + path {
	case "GET /authorize/signin":
		w.Header().Add("Set-Cookie", "aasp=aasp-value; Path=/; Secure; HttpOnly")
		reply(w, 200, nil)
	case "POST /federate":
		reply(w, 200, map[string]any{"federated": false})
	case "POST /signin/init":
		if r.Header.Get("scnt") == "" || !hasCookie(r, "aasp") {
			reply(w, 400, serviceError("-1", "session headers or cookies missing"))
			return
		}
		if str("accountName") != AppleID {
			reply(w, 401, serviceError("-20101", "Your Apple Account or password was incorrect."))
			return
		}
		a, _ := base64.StdEncoding.DecodeString(str("a"))
		srv, err := srp.NewServer([]byte(AppleID), salt, srp.Verifier(salt, passKey(f.Protocol, salt, iterations)), a)
		if err != nil {
			reply(w, 400, nil)
			return
		}
		f.srp = srv
		reply(w, 200, map[string]any{
			"iteration": iterations, "protocol": f.Protocol, "c": "challenge-id",
			"salt": base64.StdEncoding.EncodeToString(salt),
			"b":    base64.StdEncoding.EncodeToString(srv.PublicKey()),
		})
	case "POST /signin/complete":
		m1, _ := base64.StdEncoding.DecodeString(str("m1"))
		if f.srp == nil || str("c") != "challenge-id" || !f.srp.Check(m1) {
			reply(w, 401, serviceError("-20101", "Your Apple Account or password was incorrect."))
			return
		}
		f.srp = nil
		trusted := false
		tokens, _ := body["trustTokens"].([]any)
		for _, t := range tokens {
			trusted = trusted || (f.trustToken != "" && t == f.trustToken)
		}
		if trusted {
			f.sessionToken = f.next("session")
			w.Header().Set("X-Apple-Session-Token", f.sessionToken)
			w.Header().Set("X-Apple-ID-Account-Country", "USA")
			reply(w, 200, map[string]any{"authType": "hsa2"})
			return
		}
		w.Header().Set("X-Apple-ID-Account-Country", "USA")
		reply(w, 409, map[string]any{"authType": "hsa2"})
	case "GET ":
		opts := map[string]any{"trustedPhoneNumbers": []map[string]any{
			{"id": 7, "numberWithDialCode": Phone, "pushMode": map[bool]string{true: "sms", false: "push"}[f.SMSOnly]},
		}}
		if f.NoTrustedDevices != nil {
			opts["noTrustedDevices"] = *f.NoTrustedDevices
		}
		if f.SecurityKey {
			opts["fsaChallenge"] = map[string]any{"challenge": "abc", "rpId": "apple.com"}
			opts["keyNames"] = []string{"YubiKey"}
		}
		reply(w, 200, opts)
	case "PUT /verify/trusteddevice/securitycode":
		f.codePrompts++
		reply(w, 202, nil)
	case "POST /verify/trusteddevice/securitycode":
		checkCode()
	case "PUT /verify/phone":
		f.codePrompts++
		reply(w, 200, nil)
	case "POST /verify/phone/securitycode":
		if str("mode") != "sms" {
			reply(w, 400, nil)
			return
		}
		checkCode()
	case "GET /2sv/trust":
		if f.RefuseTrust {
			reply(w, 400, serviceError("-1", "trust refused"))
			return
		}
		if f.sessionToken == "" {
			reply(w, 401, nil)
			return
		}
		f.trustToken = f.next("trust")
		w.Header().Set("X-Apple-TwoSV-Trust-Token", f.trustToken)
		reply(w, 204, nil)
	default:
		reply(w, 404, nil)
	}
}

func hasCookie(r *http.Request, name string) bool {
	_, err := r.Cookie(name)
	return err == nil
}

func (f *Fake) serveHME(w http.ResponseWriter, op string, str func(string) string) {
	ok := func(result any) { reply(w, 200, map[string]any{"success": true, "result": result}) }
	fail := func(code any, msg string) {
		reply(w, 200, map[string]any{"success": false, "error": map[string]any{"errorCode": code, "errorMessage": msg}})
	}
	find := func() *Alias {
		for i := range f.aliases {
			if f.aliases[i].ID == str("anonymousId") {
				return &f.aliases[i]
			}
		}
		return nil
	}

	switch op {
	case "v2/hme/list":
		listing := map[string]any{"hmeEmails": f.aliases, "selectedForwardTo": f.forwardTo}
		if !f.OmitForwardList {
			listing["forwardToEmails"] = []string{"tester@icloud.com", "tester@example.com"}
		}
		ok(listing)
	case "v1/hme/generate":
		addr := f.next("fresh-alias") + "@icloud.com"
		if f.NewAddress != nil {
			addr = f.NewAddress()
		}
		ok(map[string]any{"hme": addr})
	case "v1/hme/reserve":
		if str("label") == "" {
			fail("-41015", "label is required")
			return
		}
		a := Alias{ID: f.next("id"), Address: str("hme"), Label: str("label"), Note: str("note"),
			Active: true, ForwardTo: f.forwardTo, Created: 1_720_000_000_000}
		if f.Now != nil {
			a.Created = f.Now().UnixMilli()
		}
		f.aliases = append(f.aliases, a)
		ok(map[string]any{"hme": a})
	case "v1/hme/updateMetaData", "v1/hme/deactivate", "v1/hme/reactivate", "v1/hme/delete":
		a := find()
		if a == nil {
			fail(-41000, "no such address")
			return
		}
		switch op {
		case "v1/hme/updateMetaData":
			a.Label, a.Note = str("label"), str("note")
		case "v1/hme/deactivate":
			a.Active = false
		case "v1/hme/reactivate":
			a.Active = true
		case "v1/hme/delete":
			if a.Active {
				fail("-41020", "address must be deactivated before it is deleted")
				return
			}
			if a.ID == f.FailDeleteOf {
				fail("-41099", "delete failed")
				return
			}
			id := a.ID
			kept := f.aliases[:0]
			for _, x := range f.aliases {
				if x.ID != id {
					kept = append(kept, x)
				}
			}
			f.aliases = kept
		}
		ok(map[string]any{})
	case "v1/hme/updateForwardTo":
		f.forwardTo = str("forwardToEmail")
		ok(map[string]any{})
	default:
		reply(w, 404, nil)
	}
}
