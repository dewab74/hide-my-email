// Package icloud is a client for the private iCloud web API behind
// Hide My Email: Apple's SRP sign-in with two-factor codes, session
// resumption, and the alias endpoints. Apple publishes no API for this;
// the client speaks what icloud.com speaks.
package icloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	defaultAuthBase = "https://idmsa.apple.com/appleauth/auth"
	defaultSetup    = "https://setup.icloud.com/setup/ws/1"
	defaultSetupCN  = "https://setup.icloud.com.cn/setup/ws/1"

	// widgetKey is the public OAuth client id of the iCloud web app.
	// Every browser that opens icloud.com sends it; it is not a secret.
	widgetKey = "d39ba9916b7251055b22c7f910e2ea796ee65e98b2ddecea8f5dde8d9d1a815d" // gitleaks:allow  pragma: allowlist secret

	// buildNumber is decoration the web client sends. Apple does not
	// check it, so a 401 is never cured by bumping it.
	buildNumber = "2624Build22"

	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.3.1 Safari/605.1.15"

	maxResponseBytes = 4 << 20
	defaultTimeout   = 30 * time.Second
	// validateTTL is how long a session Apple confirmed is trusted
	// without asking again. A stale one is caught and recovered by the
	// first service call anyway.
	validateTTL = 10 * time.Minute
)

// Client talks to iCloud on behalf of one session.
type Client struct {
	hc        *http.Client
	jar       *jar
	sess      *Session
	authBase  string
	setupBase string // "" picks the region default
	insecure  bool
	frameID   string
	authAttr  string
	// fresh means Apple minted this session in this process. If a
	// service refuses a fresh session, minting another cannot help.
	fresh   bool
	account *accountInfo
	changed bool
	debug   io.Writer
}

// Option configures a Client.
type Option func(*Client)

// WithDebug traces requests to w: method, host, path and status only.
// Headers, bodies and query strings are never written.
func WithDebug(w io.Writer) Option { return func(c *Client) { c.debug = w } }

// WithTimeout sets the per-request timeout.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.hc.Timeout = d } }

// WithTestEndpoints points the client at a fake Apple and lifts the
// HTTPS and Apple-host restrictions. For tests only.
func WithTestEndpoints(authBase, setupBase string) Option {
	return func(c *Client) {
		c.authBase, c.setupBase = authBase, setupBase
		c.insecure, c.jar.insecure = true, true
	}
}

// New returns a client for sess, or for a new empty session if nil.
func New(sess *Session, opts ...Option) *Client {
	if sess == nil {
		sess = &Session{}
	}
	j := &jar{sess: sess}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	c := &Client{
		sess:     sess,
		jar:      j,
		authBase: defaultAuthBase,
		hc: &http.Client{
			Jar:       j,
			Transport: tr,
			Timeout:   defaultTimeout,
			// Nothing in this API redirects. Following one could carry
			// credentials somewhere they were not meant to go.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	for _, o := range opts {
		o(c)
	}
	if sess.ClientID == "" {
		sess.ClientID = newUUID()
		c.changed = true
	}
	return c
}

// Session returns the live session, for persisting.
func (c *Client) Session() *Session { return c.sess }

// Changed reports whether the session differs from what New was given
// and so needs saving.
func (c *Client) Changed() bool {
	c.jar.mu.Lock()
	defer c.jar.mu.Unlock()
	return c.changed || c.jar.changed
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// checkURL enforces that requests only ever go to Apple over HTTPS.
// Some URLs come out of Apple's own responses, so they are checked
// here rather than trusted.
func (c *Client) checkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("malformed service URL")
	}
	if c.insecure {
		return u, nil
	}
	if u.Scheme != "https" || u.User != nil || !isAppleHost(u.Hostname()) {
		return nil, fmt.Errorf("refusing to contact %q: not an Apple HTTPS endpoint", u.Host)
	}
	return u, nil
}

func (c *Client) send(ctx context.Context, method, rawURL string, headers map[string]string, body any) (*response, error) {
	u, err := c.checkURL(rawURL)
	if err != nil {
		return nil, err
	}
	endpoint := u.Host + u.Path

	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", userAgent)

	if c.debug != nil {
		fmt.Fprintf(c.debug, "> %s %s\n", method, endpoint)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// url.Error would print the full URL, query string included.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("request to %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", endpoint, err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("response from %s is unreasonably large", endpoint)
	}
	if c.debug != nil {
		fmt.Fprintf(c.debug, "< %d (%d bytes)\n", resp.StatusCode, len(data))
	}
	return &response{status: resp.StatusCode, header: resp.Header, body: data}, nil
}

var serviceHeaders = map[string]string{
	"Accept":       "application/json",
	"Content-Type": "application/json",
	"Origin":       "https://www.icloud.com",
	"Referer":      "https://www.icloud.com/",
}

// service calls an iCloud web service with the session cookies. A
// non-2xx answer comes back as *HTTPError together with the body.
func (c *Client) service(ctx context.Context, method, rawURL string, body any) ([]byte, error) {
	resp, err := c.send(ctx, method, rawURL, serviceHeaders, body)
	if err != nil {
		return nil, err
	}
	if resp.status < 200 || resp.status > 299 {
		u, _ := url.Parse(rawURL)
		return resp.body, &HTTPError{Status: resp.status, Endpoint: u.Host + u.Path, Detail: errorDetail(resp.body)}
	}
	return resp.body, nil
}

// errorDetail pulls a human-readable reason out of an error body, if
// Apple gave one. Raw bodies are not echoed: they can be HTML or carry
// account details.
func errorDetail(body []byte) string {
	var parsed struct {
		Reason string `json:"reason"`
		Error  any    `json:"error"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return ""
	}
	if parsed.Reason != "" {
		return clean(parsed.Reason, 200)
	}
	if s, ok := parsed.Error.(string); ok {
		return clean(s, 200)
	}
	return ""
}

func (c *Client) setupURL() string {
	switch {
	case c.setupBase != "":
		return c.setupBase
	case c.sess.AccountCountry == "CN":
		return defaultSetupCN
	}
	return defaultSetup
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
