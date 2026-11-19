package icloud_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dewab74/hide-my-email/internal/icloud"
	"github.com/dewab74/hide-my-email/internal/icloud/icloudtest"
)

var ctx = context.Background()

func giveCode(code string) icloud.CodeFunc {
	return func(context.Context, icloud.TwoFactorRequest) (string, error) { return code, nil }
}

func newClient(f *icloudtest.Fake, sess *icloud.Session, opts ...icloud.Option) *icloud.Client {
	return icloud.New(sess, append(opts, icloud.WithTestEndpoints(f.AuthBase(), f.SetupBase()))...)
}

func signedIn(t *testing.T, f *icloudtest.Fake) *icloud.Client {
	t.Helper()
	c := newClient(f, nil)
	if err := c.Login(ctx, icloudtest.AppleID, []byte(icloudtest.Password), giveCode(icloudtest.Code)); err != nil {
		t.Fatalf("Login: %v", err)
	}
	return c
}

func TestLoginWithDeviceCode(t *testing.T) {
	f := icloudtest.New(t)
	var asked icloud.TwoFactorRequest
	c := newClient(f, nil)
	err := c.Login(ctx, "  Tester@Example.com ", []byte(icloudtest.Password),
		func(_ context.Context, req icloud.TwoFactorRequest) (string, error) {
			asked = req
			return icloudtest.Code, nil
		})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if asked.Method != "device" {
		t.Errorf("2FA method = %q, want device", asked.Method)
	}
	if f.CodePrompts() != 1 {
		t.Errorf("code pushes requested = %d, want 1", f.CodePrompts())
	}
	s := c.Session()
	if s.AppleID != icloudtest.AppleID || s.SessionToken == "" || s.TrustToken == "" || s.DSID == "" || s.HMEBaseURL == "" || len(s.Cookies) == 0 {
		t.Errorf("session incomplete after login: %+v", s)
	}
	if !c.Changed() {
		t.Error("Changed() = false after login")
	}
	if l, err := c.List(ctx); err != nil || len(l.Aliases) != 2 {
		t.Errorf("List after login: %v, %v", l, err)
	}
}

func TestLoginWithSMSCode(t *testing.T) {
	f := icloudtest.New(t)
	f.SMSOnly = true
	var asked icloud.TwoFactorRequest
	c := newClient(f, nil)
	err := c.Login(ctx, icloudtest.AppleID, []byte(icloudtest.Password),
		func(_ context.Context, req icloud.TwoFactorRequest) (string, error) {
			asked = req
			return icloudtest.Code, nil
		})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if asked.Method != "sms" || asked.Destination != icloudtest.Phone {
		t.Errorf("2FA request = %+v", asked)
	}
}

func TestLoginLegacyPasswordProtocol(t *testing.T) {
	f := icloudtest.New(t)
	f.Protocol = "s2k_fo"
	signedIn(t, f)
}

func TestLoginFailures(t *testing.T) {
	cases := []struct {
		name, id, password, code string
		setup                    func(*icloudtest.Fake)
		want                     string
	}{
		{name: "wrong password", id: icloudtest.AppleID, password: "nope", code: icloudtest.Code, want: "incorrect Apple ID or password"},
		{name: "unknown account", id: "other@example.com", password: icloudtest.Password, code: icloudtest.Code, want: "refused sign-in"},
		{name: "wrong code", id: icloudtest.AppleID, password: icloudtest.Password, code: "000000", want: "incorrect verification code"},
		{name: "security key", id: icloudtest.AppleID, password: icloudtest.Password, code: icloudtest.Code,
			setup: func(f *icloudtest.Fake) { f.SecurityKey = true }, want: "security key"},
		{name: "unknown protocol", id: icloudtest.AppleID, password: icloudtest.Password, code: icloudtest.Code,
			setup: func(f *icloudtest.Fake) { f.Protocol = "s3k" }, want: "unsupported password protocol"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := icloudtest.New(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			c := newClient(f, nil)
			err := c.Login(ctx, tc.id, []byte(tc.password), giveCode(tc.code))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if strings.Contains(err.Error(), tc.password) {
				t.Error("the error message contains the password")
			}
		})
	}
}

func TestTrustTokenSkipsSecondFactor(t *testing.T) {
	f := icloudtest.New(t)
	first := signedIn(t, f)

	again := newClient(f, &icloud.Session{TrustToken: first.Session().TrustToken, ClientID: first.Session().ClientID})
	err := again.Login(ctx, icloudtest.AppleID, []byte(icloudtest.Password),
		func(context.Context, icloud.TwoFactorRequest) (string, error) {
			t.Error("asked for a code despite a valid trust token")
			return "", errors.New("unexpected")
		})
	if err != nil {
		t.Fatalf("second Login: %v", err)
	}
}

// roundTrip persists and restores a session the way the CLI does.
func roundTrip(t *testing.T, f *icloudtest.Fake, c *icloud.Client) *icloud.Client {
	t.Helper()
	data, err := json.Marshal(c.Session())
	if err != nil {
		t.Fatal(err)
	}
	var sess icloud.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		t.Fatal(err)
	}
	return newClient(f, &sess)
}

func TestStoredSessionResumes(t *testing.T) {
	f := icloudtest.New(t)
	c := roundTrip(t, f, signedIn(t, f))
	if err := c.Resume(ctx); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := c.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}
	if c.Changed() {
		t.Error("an untouched, recently validated session was marked changed")
	}
	if slices.Contains(f.Requests(), "POST /setup/validate") {
		t.Error("a fresh session was revalidated")
	}
	if err := c.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !slices.Contains(f.Requests(), "POST /setup/validate") {
		t.Error("Refresh did not ask Apple")
	}
}

func TestRecoversFromExpiredCookies(t *testing.T) {
	f := icloudtest.New(t)
	c := roundTrip(t, f, signedIn(t, f))
	f.ExpireCookies()

	// The service refuses; the client re-mints cookies and replays.
	al, err := c.Create(ctx, "After expiry", "")
	if err != nil {
		t.Fatalf("Create after cookie expiry: %v", err)
	}
	if al.Label != "After expiry" || !c.Changed() {
		t.Errorf("alias = %+v, changed = %v", al, c.Changed())
	}

	// And Refresh takes the same road when validate is refused.
	c2 := roundTrip(t, f, c)
	f.ExpireCookies()
	if err := c2.Refresh(ctx); err != nil {
		t.Fatalf("Refresh after cookie expiry: %v", err)
	}
}

func TestRevokedSessionAsksForLogin(t *testing.T) {
	f := icloudtest.New(t)
	c := roundTrip(t, f, signedIn(t, f))
	f.RevokeSession()

	_, err := c.List(ctx)
	if !errors.Is(err, icloud.ErrSessionExpired) || !icloud.IsAuthError(err) {
		t.Errorf("List: err = %v, want ErrSessionExpired", err)
	}
	if err := c.Refresh(ctx); !errors.Is(err, icloud.ErrSessionExpired) {
		t.Errorf("Refresh: err = %v, want ErrSessionExpired", err)
	}
	if err := newClient(f, nil).Resume(ctx); !errors.Is(err, icloud.ErrNotLoggedIn) {
		t.Errorf("Resume without a session: err = %v", err)
	}
}

func TestAliasLifecycle(t *testing.T) {
	f := icloudtest.New(t)
	c := signedIn(t, f)

	al, err := c.Create(ctx, "Shop", "kettle")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(al.Address, "@icloud.com") || al.Label != "Shop" || al.Note != "kettle" || !al.Active || al.ID == "" {
		t.Fatalf("created alias = %+v", al)
	}

	if err := c.Update(ctx, al.ID, "Shop 2", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, al.ID); err == nil {
		t.Error("deleting an active alias succeeded")
	} else {
		var apiErr *icloud.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "-41020" {
			t.Errorf("delete error = %v", err)
		}
	}
	if err := c.Deactivate(ctx, al.ID); err != nil {
		t.Fatal(err)
	}
	l, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := l.Aliases[len(l.Aliases)-1]
	if got.Label != "Shop 2" || got.Note != "" || got.Active {
		t.Errorf("after update+deactivate: %+v", got)
	}
	if err := c.Reactivate(ctx, al.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.Deactivate(ctx, al.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, al.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.Aliases()) != 2 {
		t.Errorf("aliases left = %d, want 2", len(f.Aliases()))
	}

	// A numeric error code decodes as well as a string one.
	err = c.Deactivate(ctx, "no-such-id")
	var apiErr *icloud.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "-41000" || apiErr.Message != "no such address" {
		t.Errorf("unknown id: err = %v", err)
	}

	if err := c.SetForwardTo(ctx, "tester@example.com"); err != nil {
		t.Fatal(err)
	}
	if l, _ := c.List(ctx); l.ForwardTo != "tester@example.com" || len(l.ForwardToOptions) != 2 {
		t.Errorf("forwarding = %+v", l)
	}
}

func TestAccountWithoutHideMyEmail(t *testing.T) {
	f := icloudtest.New(t)
	f.NoHME = true
	c := signedIn(t, f)
	if _, err := c.List(ctx); err == nil || !strings.Contains(err.Error(), "iCloud+") {
		t.Errorf("err = %v, want an explanation about iCloud+", err)
	}
}

func TestDebugTraceLeaksNoSecrets(t *testing.T) {
	f := icloudtest.New(t)
	var trace bytes.Buffer
	c := newClient(f, nil, icloud.WithDebug(&trace))
	if err := c.Login(ctx, icloudtest.AppleID, []byte(icloudtest.Password), giveCode(icloudtest.Code)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(ctx); err != nil {
		t.Fatal(err)
	}
	out := trace.String()
	if !strings.Contains(out, "/v2/hme/list") {
		t.Fatalf("trace is missing requests:\n%s", out)
	}
	s := c.Session()
	for name, secret := range map[string]string{
		"password": icloudtest.Password, "code": icloudtest.Code, "session token": s.SessionToken,
		"trust token": s.TrustToken, "dsid": s.DSID, "client id": s.ClientID, "cookie": s.Cookies[0].Value,
		"apple id": icloudtest.AppleID, "query string": "?",
	} {
		if strings.Contains(out, secret) {
			t.Errorf("the debug trace contains the %s", name)
		}
	}
}
