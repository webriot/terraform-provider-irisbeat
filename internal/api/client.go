// SPDX-License-Identifier: AGPL-3.0-only
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxResponse = 64 << 10
)

var (
	ErrMonitorMissing = errors.New("the Monitor does not exist in this Irisbeat installation")
	errUnexpected     = errors.New("Irisbeat returned an unexpected response")
	errTransport      = errors.New("unable to complete the request to Irisbeat")
)

type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

func New(baseURL, key string) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") ||
		base.User != nil || base.ForceQuery || base.RawQuery != "" || strings.Contains(baseURL, "#") {
		return nil, errors.New("base_url must be an HTTP or HTTPS URL without credentials, query, or fragment")
	}
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, " \t\r\n") {
		return nil, errors.New("api_key must contain an Irisbeat Write API key")
	}
	return &Client{
		baseURL: strings.TrimRight(base.String(), "/"),
		key:     key,
		http: &http.Client{
			Timeout:       8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

type Monitor struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Kind              string   `json:"kind"`
	URL               string   `json:"url"`
	Hostname          string   `json:"hostname"`
	Port              int64    `json:"port"`
	Published         *bool    `json:"published"`
	ScheduleMode      string   `json:"scheduleMode"`
	IntervalSeconds   int64    `json:"intervalSeconds"`
	TimeoutSeconds    int64    `json:"timeoutSeconds"`
	FailureThreshold  int64    `json:"failureThreshold"`
	RecoveryThreshold int64    `json:"recoveryThreshold"`
	Cron              string   `json:"cron"`
	Timezone          string   `json:"timezone"`
	DestinationIDs    []string `json:"destinationIds"`

	SilenceSeconds         int64 `json:"silenceSeconds"`
	RecoverySpacingSeconds int64 `json:"recoverySpacingSeconds"`
	GraceSeconds           int64 `json:"graceSeconds"`
}

type endpoint[T any] struct {
	path, label, missingMessage, invalidConfig string
	missing                                    error
	valid                                      func(T, string) bool
	classify                                   func(*http.Response) error
}

var monitors = endpoint[Monitor]{
	path: "/api/v1/monitors", label: "Monitor", missingMessage: "Monitor not found", missing: ErrMonitorMissing,
	invalidConfig: "Check the target, schedule, timing, installation check floor, and destination IDs",
	classify: func(resp *http.Response) error {
		if resp.StatusCode == http.StatusBadRequest {
			var payload struct {
				Code string `json:"code"`
			}
			if decode(resp.Body, &payload) && payload.Code == "destination_pending_confirmation" {
				return errors.New("an email destination is awaiting confirmation. Ask the recipient to confirm the address, then reapply.")
			}
		}
		return nil
	},
	valid: func(m Monitor, id string) bool {
		return m.ID != "" && (id == "" || m.ID == id) && m.Name != "" &&
			m.Kind != "" && m.Published != nil && m.DestinationIDs != nil &&
			((m.Kind != "http" && m.Kind != "tcp") || validOutboundMonitor(m)) &&
			(m.Kind != "heartbeat" || validHeartbeatMonitor(m))
	},
}

func (c *Client) CreateMonitor(ctx context.Context, fields map[string]any) (Monitor, error) {
	kind, _ := fields["kind"].(string)
	if kind == "" {
		kind = "http"
	}
	ep := monitors
	ep.valid = func(m Monitor, id string) bool { return m.Kind == kind && monitors.valid(m, id) }
	return create(ctx, c, ep, fields)
}

func (c *Client) ReadMonitor(ctx context.Context, id string) (Monitor, error) {
	monitor, _, err := request(ctx, c, monitors, http.MethodGet, id, "", nil, http.StatusOK)
	return monitor, err
}

func (c *Client) UpdateMonitor(ctx context.Context, id string, fields map[string]any) (Monitor, error) {
	return update(ctx, c, monitors, id, fields)
}

func (c *Client) DeleteMonitor(ctx context.Context, id string) error {
	_, _, err := request(ctx, c, monitors, http.MethodDelete, id, "", nil, http.StatusNoContent)
	return err
}

func create[T any](ctx context.Context, c *Client, ep endpoint[T], fields map[string]any) (T, error) {
	var zero T
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return zero, errors.New("unable to generate a create idempotency key")
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return zero, fmt.Errorf("unable to encode the %s configuration", ep.label)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	idempotencyKey := hex.EncodeToString(key)
	unknown := fmt.Errorf("create outcome is unknown. Check Irisbeat and import any existing %s by ID before trying again", ep.label)
	uncertain := false
	for attempt := 0; attempt < 3; attempt++ {
		saved, retry, err := request(ctx, c, ep, http.MethodPost, "", idempotencyKey, body, http.StatusCreated)
		if err == nil {
			return saved, nil
		}
		if !retry {
			if uncertain {
				return zero, fmt.Errorf("%w\n\nThe %w", err, unknown)
			}
			return zero, err
		}
		uncertain = true
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return zero, unknown
			}
		}
	}
	return zero, unknown
}

func update[T any](ctx context.Context, c *Client, ep endpoint[T], id string, fields map[string]any) (T, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("unable to encode the %s configuration", ep.label)
	}
	saved, _, err := request(ctx, c, ep, http.MethodPatch, id, "", body, http.StatusOK)
	return saved, err
}

func request[T any](ctx context.Context, c *Client, ep endpoint[T], method, id, key string, body []byte, expectedStatus int) (T, bool, error) {
	var zero T
	target := c.baseURL + ep.path
	if method != http.MethodPost {
		if id == "" || id == "." || id == ".." {
			return zero, false, fmt.Errorf("a valid %s ID is required", ep.label)
		}
		target += "/" + url.PathEscape(id)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return zero, false, errUnexpected
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
		req.GetBody = nil // All POST retries share this operation's explicit attempt budget.
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return zero, true, errTransport
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		err := responseError(resp, id != "", ep)
		retry := resp.StatusCode >= 500 || resp.StatusCode == 408 || resp.StatusCode == 425 ||
			resp.StatusCode == 429 || (resp.StatusCode >= 200 && resp.StatusCode < 300)
		return zero, retry, err
	}
	if expectedStatus == http.StatusNoContent {
		return zero, false, nil
	}
	var saved T
	if !decode(resp.Body, &saved) || !ep.valid(saved, id) {
		return zero, true, errUnexpected
	}
	return saved, false, nil
}

func validOutboundMonitor(m Monitor) bool {
	if m.Kind == "http" && m.URL == "" || m.Kind == "tcp" && (m.Hostname == "" || m.Port < 1 || m.Port > 65535) {
		return false
	}
	if m.IntervalSeconds < 1 || m.TimeoutSeconds < 1 || m.FailureThreshold < 1 || m.RecoveryThreshold < 1 {
		return false
	}
	return m.ScheduleMode == "interval" || (m.ScheduleMode == "calendar" && m.Cron != "" && m.Timezone != "")
}

func validHeartbeatMonitor(m Monitor) bool {
	if m.RecoveryThreshold < 1 {
		return false
	}
	switch m.ScheduleMode {
	case "interval":
		return m.IntervalSeconds > 0 && m.SilenceSeconds > 0 && m.RecoverySpacingSeconds > 0
	case "calendar":
		return m.Cron != "" && m.Timezone != "" && m.GraceSeconds > 0
	default:
		return false
	}
}

func decode(body io.Reader, value any) bool {
	data, err := io.ReadAll(io.LimitReader(body, maxResponse+1))
	return err == nil && len(data) <= maxResponse && json.Unmarshal(data, value) == nil
}

func responseError[T any](resp *http.Response, resource bool, ep endpoint[T]) error {
	if err := ep.classify(resp); err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return errors.New("Irisbeat rejected the API key as invalid, expired, or revoked")
	case http.StatusForbidden:
		return errors.New("the API key has insufficient permission. Use an Irisbeat Write API key")
	case http.StatusBadRequest:
		return fmt.Errorf("Irisbeat rejected the %s configuration. %s", ep.label, ep.invalidConfig)
	case http.StatusConflict:
		return errors.New("Irisbeat reported a conflict. Check the installation before trying again")
	case http.StatusNotFound:
		mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		var payload struct {
			Error string `json:"error"`
		}
		if resource && mediaType == "application/json" && decode(resp.Body, &payload) && payload.Error == ep.missingMessage {
			return ep.missing
		}
	}
	return fmt.Errorf("Irisbeat request failed (HTTP %d)", resp.StatusCode)
}
