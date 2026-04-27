// Package cloudflare wraps the Cloudflare DNS API for the small surface this
// service needs: creating, looking up, and deleting per-host A records under
// the configured zone.
//
// The package intentionally does not import any third-party Cloudflare SDK.
// Three functions on a tiny REST surface don't justify a 30-MB transitive dep
// tree, and a handwritten client makes the auth/error semantics easier to
// reason about during outages.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const apiBase = "https://api.cloudflare.com/client/v4"

type Client struct {
	token  string
	zoneID string
	http   *http.Client
}

// New returns a Cloudflare client scoped to a single zone. The token must
// have Zone:DNS:Edit on the given zone.
func New(token, zoneID string) *Client {
	return &Client{
		token:  token,
		zoneID: zoneID,
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e apiError) Error() string { return fmt.Sprintf("cloudflare error %d: %s", e.Code, e.Message) }

// DNSRecord is the subset of fields we read/write. Cloudflare returns more,
// but we don't touch them.
type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied bool   `json:"proxied"`
}

// CreateARecord adds an A record `name -> ip` and returns its ID. `name`
// is the FQDN (e.g. "host-abc.gs.elomm.net"); Cloudflare normalizes either
// FQDNs or bare labels to the FQDN within the zone.
//
// TTL is 60s so any client that does cache the record sees an updated IP
// within a minute. The record is NOT proxied — game-server traffic goes
// direct to the Hetzner host.
func (c *Client) CreateARecord(ctx context.Context, name, ip string) (recordID string, err error) {
	body, _ := json.Marshal(DNSRecord{
		Type:    "A",
		Name:    name,
		Content: ip,
		TTL:     60,
		Proxied: false,
	})
	var rec DNSRecord
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", c.zoneID), body, &rec); err != nil {
		return "", err
	}
	return rec.ID, nil
}

// DeleteARecord removes a DNS record by ID. Returns nil if the record is
// already gone (Cloudflare returns 404, we swallow it) so the caller doesn't
// have to special-case re-runs.
func (c *Client) DeleteARecord(ctx context.Context, recordID string) error {
	err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/zones/%s/dns_records/%s", c.zoneID, recordID), nil, nil)
	if errResp, ok := err.(*apiErrorResponse); ok && errResp.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// FindARecordByName returns the ID of an A record matching `name`, or "" if
// none. Used during host teardown when the matchmaker has lost the record ID
// (e.g. it crashed mid-CreateHost).
func (c *Client) FindARecordByName(ctx context.Context, name string) (recordID string, err error) {
	var records []DNSRecord
	path := fmt.Sprintf("/zones/%s/dns_records?type=A&name=%s", c.zoneID, name)
	if err := c.do(ctx, http.MethodGet, path, nil, &records); err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}
	return records[0].ID, nil
}

type apiErrorResponse struct {
	Status int
	Errors []apiError
}

func (e *apiErrorResponse) Error() string {
	if len(e.Errors) == 0 {
		return fmt.Sprintf("cloudflare http %d", e.Status)
	}
	return fmt.Sprintf("cloudflare http %d: %s", e.Status, e.Errors[0].Message)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)

	envelope := struct {
		Success bool            `json:"success"`
		Errors  []apiError      `json:"errors"`
		Result  json.RawMessage `json:"result"`
	}{}
	_ = json.Unmarshal(respBytes, &envelope)

	if resp.StatusCode >= 400 || !envelope.Success {
		return &apiErrorResponse{Status: resp.StatusCode, Errors: envelope.Errors}
	}
	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("decode cloudflare result: %w", err)
		}
	}
	return nil
}
