package icloud

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Session is everything needed to talk to iCloud again without the
// password: Apple's session and trust tokens plus the web cookies. It
// is as sensitive as the password itself and must only be persisted
// through the store package.
type Session struct {
	AppleID        string    `json:"appleId"`
	DSID           string    `json:"dsid"`
	SessionToken   string    `json:"sessionToken"`
	TrustToken     string    `json:"trustToken"`
	Scnt           string    `json:"scnt"`
	SessionID      string    `json:"sessionId"`
	AccountCountry string    `json:"accountCountry"`
	ClientID       string    `json:"clientId"`
	HMEBaseURL     string    `json:"hmeBaseUrl"`
	Cookies        []Cookie  `json:"cookies"`
	ValidatedAt    time.Time `json:"validatedAt"`
}

// Cookie is one stored web cookie.
type Cookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Quoted bool   `json:"quoted,omitempty"`
	// Domain is the host the cookie belongs to. With HostOnly it is
	// sent to exactly that host, otherwise to its subdomains too.
	Domain   string `json:"domain"`
	HostOnly bool   `json:"hostOnly,omitempty"`
}

// appleDomains are the only places this tool sends requests or cookies.
var appleDomains = []string{"apple.com", "icloud.com", "icloud.com.cn"}

func inDomain(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func isAppleHost(host string) bool {
	host = strings.ToLower(host)
	for _, d := range appleDomains {
		if inDomain(host, d) {
			return true
		}
	}
	return false
}

// jar is an http.CookieJar backed by the session, so cookies survive
// between runs. It scopes cookies by domain the way a browser does and
// refuses a Domain attribute that reaches outside Apple.
type jar struct {
	mu       sync.Mutex
	sess     *Session
	insecure bool // tests only: plain HTTP and non-Apple hosts
	changed  bool
}

func (j *jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	host := strings.ToLower(u.Hostname())
	now := time.Now()

	for _, ck := range cookies {
		entry := Cookie{Name: ck.Name, Value: ck.Value, Quoted: ck.Quoted, Domain: host, HostOnly: true}
		if d := strings.ToLower(strings.TrimPrefix(ck.Domain, ".")); d != "" && d != host {
			if !inDomain(host, d) || (!j.insecure && !isAppleHost(d)) {
				continue // not this host's cookie to set
			}
			entry.Domain, entry.HostOnly = d, false
		}

		expired := ck.Value == "" || ck.MaxAge < 0 || (!ck.Expires.IsZero() && ck.Expires.Before(now))
		idx := -1
		for i, have := range j.sess.Cookies {
			if have.Name == entry.Name && have.Domain == entry.Domain {
				idx = i
				break
			}
		}
		switch {
		case expired && idx >= 0:
			j.sess.Cookies = append(j.sess.Cookies[:idx], j.sess.Cookies[idx+1:]...)
			j.changed = true
		case expired:
		case idx < 0:
			j.sess.Cookies = append(j.sess.Cookies, entry)
			j.changed = true
		case j.sess.Cookies[idx] != entry:
			j.sess.Cookies[idx] = entry
			j.changed = true
		}
	}
}

func (j *jar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	if u.Scheme != "https" && !j.insecure {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	var out []*http.Cookie
	for _, ck := range j.sess.Cookies {
		if ck.Domain == host || (!ck.HostOnly && inDomain(host, ck.Domain)) {
			// A request cookie: only name and value go on the wire, so
			// the Set-Cookie attributes gosec looks for do not apply.
			out = append(out, &http.Cookie{Name: ck.Name, Value: ck.Value, Quoted: ck.Quoted}) // #nosec G124
		}
	}
	return out
}
