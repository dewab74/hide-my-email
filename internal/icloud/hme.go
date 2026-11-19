package icloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Alias is one Hide My Email address.
type Alias struct {
	ID              string `json:"anonymousId"`
	Address         string `json:"hme"`
	Label           string `json:"label"`
	Note            string `json:"note"`
	Active          bool   `json:"isActive"`
	ForwardTo       string `json:"forwardToEmail"`
	Origin          string `json:"origin"`
	CreateTimestamp int64  `json:"createTimestamp"` // milliseconds
}

// Created is when the alias was made.
func (a Alias) Created() time.Time { return time.UnixMilli(a.CreateTimestamp) }

// Listing is the account's aliases and forwarding setup.
type Listing struct {
	Aliases          []Alias  `json:"hmeEmails"`
	ForwardTo        string   `json:"selectedForwardTo"`
	ForwardToOptions []string `json:"forwardToEmails"`
}

// hme performs one Hide My Email call and decodes its result into out.
// If the service refuses the session, it is refreshed once and the call
// replayed: a refused call changed nothing on Apple's side, so that is
// safe for the mutating calls too.
func (c *Client) hme(ctx context.Context, op, method string, version int, body, out any) error {
	data, err := c.hmeOnce(ctx, op, method, version, body)
	if err != nil && isRejection(err) && !c.fresh {
		if rerr := c.accountLogin(ctx); rerr != nil {
			return rerr
		}
		data, err = c.hmeOnce(ctx, op, method, version, body)
	}
	if err != nil {
		if isRejection(err) {
			// Apple just confirmed the session, so this is the service
			// refusing the account, not an expiry.
			return fmt.Errorf("iCloud accepted the session but Hide My Email refused it: %w", err)
		}
		return fmt.Errorf("%s: %w", op, err)
	}

	var env struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Error   *envelopeError  `json:"error"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("%s: could not read the response", op)
	}
	if !env.Success {
		apiErr := &APIError{Op: op}
		if env.Error != nil {
			apiErr.Code, apiErr.Message = clean(env.Error.Code, 40), clean(env.Error.Message, 300)
		}
		return apiErr
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("%s: could not read the result", op)
		}
	}
	return nil
}

func (c *Client) hmeOnce(ctx context.Context, op, method string, version int, body any) ([]byte, error) {
	if c.sess.HMEBaseURL == "" {
		return nil, c.hmeUnavailable()
	}
	q := url.Values{
		"clientBuildNumber":     {buildNumber},
		"clientMasteringNumber": {buildNumber},
		"clientId":              {c.sess.ClientID},
		"dsid":                  {c.sess.DSID},
	}
	u := fmt.Sprintf("%s/v%d/hme/%s?%s", c.sess.HMEBaseURL, version, op, q.Encode())
	return c.service(ctx, method, u, body)
}

// List returns every alias on the account.
func (c *Client) List(ctx context.Context) (*Listing, error) {
	var l Listing
	if err := c.hme(ctx, "list", "GET", 2, nil, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// Create makes a new alias. Apple does this in two steps: it proposes
// an address, which only becomes real once reserved with a label.
func (c *Client) Create(ctx context.Context, label, note string) (*Alias, error) {
	var gen struct {
		Hme string `json:"hme"`
	}
	if err := c.hme(ctx, "generate", "POST", 1, map[string]string{"langCode": "en-us"}, &gen); err != nil {
		return nil, err
	}
	if gen.Hme == "" {
		return nil, errors.New("generate: Apple returned no address")
	}
	var res struct {
		Hme Alias `json:"hme"`
	}
	req := map[string]string{"hme": gen.Hme, "label": label, "note": note}
	if err := c.hme(ctx, "reserve", "POST", 1, req, &res); err != nil {
		return nil, err
	}
	a := res.Hme
	if a.Address == "" { // tolerate a sparse answer
		a = Alias{Address: gen.Hme, Label: label, Note: note, Active: true, CreateTimestamp: time.Now().UnixMilli()}
	}
	return &a, nil
}

// Update replaces an alias's label and note.
func (c *Client) Update(ctx context.Context, id, label, note string) error {
	req := map[string]string{"anonymousId": id, "label": label, "note": note}
	return c.hme(ctx, "updateMetaData", "POST", 1, req, nil)
}

// Deactivate stops an alias from forwarding mail. It can be undone.
func (c *Client) Deactivate(ctx context.Context, id string) error {
	return c.hme(ctx, "deactivate", "POST", 1, map[string]string{"anonymousId": id}, nil)
}

// Reactivate turns a deactivated alias back on.
func (c *Client) Reactivate(ctx context.Context, id string) error {
	return c.hme(ctx, "reactivate", "POST", 1, map[string]string{"anonymousId": id}, nil)
}

// Delete permanently removes a deactivated alias.
func (c *Client) Delete(ctx context.Context, id string) error {
	return c.hme(ctx, "delete", "POST", 1, map[string]string{"anonymousId": id}, nil)
}

// SetForwardTo changes the inbox all aliases forward to.
func (c *Client) SetForwardTo(ctx context.Context, email string) error {
	return c.hme(ctx, "updateForwardTo", "POST", 1, map[string]string{"forwardToEmail": email}, nil)
}
