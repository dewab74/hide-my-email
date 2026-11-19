package icloud

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOnlyAppleHTTPSEndpointsAreContacted(t *testing.T) {
	c := New(nil)
	for _, bad := range []string{
		"http://setup.icloud.com/setup/ws/1",
		"https://evil.example/v1/hme/list",
		"https://icloud.com.evil.example/",
		"https://notapple.com/",
		"https://user:pw@icloud.com/", // pragma: allowlist secret
		"ftp://icloud.com/",
		"https://169.254.169.254/",
		"://",
	} {
		if _, err := c.checkURL(bad); err == nil {
			t.Errorf("checkURL(%q) was allowed", bad)
		}
		// And the request path enforces it, not just the helper.
		if _, err := c.send(context.Background(), "GET", bad, nil, nil); err == nil {
			t.Errorf("send(%q) was allowed", bad)
		}
	}
	for _, ok := range []string{
		"https://idmsa.apple.com/appleauth/auth",
		"https://p68-maildomainws.icloud.com:443",
		"https://setup.icloud.com.cn/setup/ws/1",
	} {
		if _, err := c.checkURL(ok); err != nil {
			t.Errorf("checkURL(%q): %v", ok, err)
		}
	}
}

func TestHostileServiceURLIsRejected(t *testing.T) {
	c := New(nil)
	body := []byte(`{"dsInfo":{"dsid":"1"},"webservices":{"premiummailsettings":{"url":"https://attacker.example"}}}`)
	if err := c.absorbAccount(body); err == nil || c.sess.HMEBaseURL != "" {
		t.Errorf("a non-Apple Hide My Email URL was accepted: %q, %v", c.sess.HMEBaseURL, err)
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func cookieNames(cs []*http.Cookie) string {
	var names []string
	for _, c := range cs {
		names = append(names, c.Name)
	}
	return strings.Join(names, ",")
}

func TestJarScopesCookiesLikeABrowser(t *testing.T) {
	sess := &Session{}
	j := &jar{sess: sess}
	setup := mustURL("https://setup.icloud.com/setup/ws/1/accountLogin")

	j.SetCookies(setup, []*http.Cookie{
		{Name: "wide", Value: "1", Domain: ".icloud.com"},
		{Name: "hostonly", Value: "2"},
		{Name: "quoted", Value: "v=1:t=x", Quoted: true, Domain: "icloud.com"},
		{Name: "foreign", Value: "3", Domain: "evil.example"}, // not this host's to set
		{Name: "tld", Value: "4", Domain: "com"},              // public suffix
		{Name: "sibling", Value: "5", Domain: "apple.com"},    // other site
	})
	j.SetCookies(mustURL("https://idmsa.apple.com/appleauth/auth"), []*http.Cookie{{Name: "aasp", Value: "6"}})
	if !j.changed {
		t.Error("jar not marked changed")
	}

	for rawURL, want := range map[string]string{
		"https://setup.icloud.com/x":             "wide,hostonly,quoted",
		"https://p68-maildomainws.icloud.com/v1": "wide,quoted",
		"https://idmsa.apple.com/":               "aasp",
		"https://icloud.com.evil.example/":       "",
		"https://evilicloud.com/":                "",
		"http://setup.icloud.com/":               "", // never over plain HTTP
	} {
		if got := cookieNames(j.Cookies(mustURL(rawURL))); got != want {
			t.Errorf("cookies for %s = %q, want %q", rawURL, got, want)
		}
	}
	for _, ck := range j.Cookies(setup) {
		if ck.Name == "quoted" && !ck.Quoted {
			t.Error("the quoted flag was lost")
		}
	}

	// Apple expiring a cookie removes it; resending one changes nothing.
	j.changed = false
	j.SetCookies(setup, []*http.Cookie{{Name: "hostonly", Value: "2"}})
	if j.changed {
		t.Error("an identical cookie marked the jar changed")
	}
	j.SetCookies(setup, []*http.Cookie{
		{Name: "hostonly", Value: "", MaxAge: -1},
		{Name: "wide", Value: "gone", Domain: ".icloud.com", Expires: time.Now().Add(-time.Hour)},
	})
	if got := cookieNames(j.Cookies(setup)); got != "quoted" || !j.changed {
		t.Errorf("after expiry: cookies = %q, changed = %v", got, j.changed)
	}
}

func TestDeriveKey(t *testing.T) {
	salt := []byte("salt")
	a, err := deriveKey([]byte("pw"), salt, 10, "s2k")
	if err != nil || len(a) != 32 {
		t.Fatalf("s2k: %x, %v", a, err)
	}
	b, err := deriveKey([]byte("pw"), salt, 10, "s2k_fo")
	if err != nil || string(a) == string(b) {
		t.Fatalf("s2k_fo: %x, %v", b, err)
	}
	for _, n := range []int{0, -1, 50_000_000} {
		if _, err := deriveKey([]byte("pw"), salt, n, "s2k"); err == nil {
			t.Errorf("iteration count %d was accepted", n)
		}
	}
}

func TestSanitize(t *testing.T) {
	got := Sanitize("  shop\x1b[2J\x07 ‮evil\nline two\t!  ", 100)
	if got != "shop[2J evil line two !" {
		t.Errorf("Sanitize = %q", got)
	}
	if got := Sanitize("abcdef", 3); got != "abc..." {
		t.Errorf("truncation = %q", got)
	}
}

func TestEnvelopeErrorShapes(t *testing.T) {
	for in, want := range map[string]envelopeError{
		`{"errorCode":"-41015","errorMessage":"nope"}`: {Code: "-41015", Message: "nope"},
		`{"errorCode":7,"reason":"why"}`:               {Code: "7", Message: "why"},
		`1`:                                            {Code: "1"},
		`"bad"`:                                        {Code: "bad"},
	} {
		var got envelopeError
		if err := got.UnmarshalJSON([]byte(in)); err != nil || got != want {
			t.Errorf("%s decoded to %+v (%v), want %+v", in, got, err, want)
		}
	}
}
