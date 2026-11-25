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
	monitorPath = "/api/v1/monitors"
	maxResponse = 64 << 10
)

var (
	ErrMonitorMissing = errors.New("the Monitor does not exist in this Irisbeat installation")
	errCreateUnknown  = errors.New("create outcome is unknown. Check Irisbeat and import any existing Monitor by ID before trying again")
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
	ID                string `json:"id"`
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	URL               string `json:"url"`
	Published         *bool  `json:"published"`
	ScheduleMode      string `json:"scheduleMode"`
	IntervalSeconds   int64  `json:"intervalSeconds"`
	TimeoutSeconds    int64  `json:"timeoutSeconds"`
	FailureThreshold  int64  `json:"failureThreshold"`
	RecoveryThreshold int64  `json:"recoveryThreshold"`
	Cron              string `json:"cron"`
	Timezone          string `json:"timezone"`

	SilenceSeconds         int64 `json:"silenceSeconds"`
	RecoverySpacingSeconds int64 `json:"recoverySpacingSeconds"`
	GraceSeconds           int64 `json:"graceSeconds"`
}

func (c *Client) CreateMonitor(ctx context.Context, fields map[string]any) (Monitor, error) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return Monitor{}, errors.New("unable to generate a create idempotency key")
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return Monitor{}, errors.New("unable to encode the Monitor configuration")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	idempotencyKey := hex.EncodeToString(key)
	kind := "http"
	if requested, ok := fields["kind"].(string); ok {
		kind = requested
	}
	uncertain := false
	for attempt := 0; attempt < 3; attempt++ {
		monitor, retry, err := c.request(ctx, http.MethodPost, "", idempotencyKey, body, http.StatusCreated)
		if err == nil && monitor.Kind == kind {
			return monitor, nil
		}
		// A mismatched success cannot confirm the write.
		if err == nil {
			retry = true
		}
		if !retry {
			if uncertain {
				return Monitor{}, errCreateUnknown
			}
			return Monitor{}, err
		}
		uncertain = true
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return Monitor{}, errCreateUnknown
			}
		}
	}
	return Monitor{}, errCreateUnknown
}

func (c *Client) ReadMonitor(ctx context.Context, id string) (Monitor, error) {
	monitor, _, err := c.request(ctx, http.MethodGet, id, "", nil, http.StatusOK)
	return monitor, err
}

func (c *Client) UpdateMonitor(ctx context.Context, id string, fields map[string]any) (Monitor, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		return Monitor{}, errors.New("unable to encode the Monitor configuration")
	}
	monitor, _, err := c.request(ctx, http.MethodPatch, id, "", body, http.StatusOK)
	return monitor, err
}

func (c *Client) DeleteMonitor(ctx context.Context, id string) error {
	_, _, err := c.request(ctx, http.MethodDelete, id, "", nil, http.StatusNoContent)
	return err
}

func (c *Client) request(ctx context.Context, method, id, key string, body []byte, expectedStatus int) (Monitor, bool, error) {
	endpoint := c.baseURL + monitorPath
	if method != http.MethodPost {
		if id == "" || id == "." || id == ".." {
			return Monitor{}, false, errors.New("a valid Monitor ID is required")
		}
		endpoint += "/" + url.PathEscape(id)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return Monitor{}, false, errUnexpected
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
		return Monitor{}, true, errTransport
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		err := responseError(resp, id != "")
		retry := resp.StatusCode >= 500 || resp.StatusCode == 408 || resp.StatusCode == 425 ||
			resp.StatusCode == 429 || (resp.StatusCode >= 200 && resp.StatusCode < 300)
		return Monitor{}, retry, err
	}
	if expectedStatus == http.StatusNoContent {
		return Monitor{}, false, nil
	}
	var monitor Monitor
	if !decode(resp.Body, &monitor) || monitor.ID == "" || (id != "" && monitor.ID != id) ||
		monitor.Name == "" || monitor.Kind == "" || monitor.Published == nil ||
		(monitor.Kind == "http" && !validHTTPMonitor(monitor)) ||
		(monitor.Kind == "heartbeat" && !validHeartbeatMonitor(monitor)) {
		return Monitor{}, true, errUnexpected
	}
	return monitor, false, nil
}

func validHTTPMonitor(m Monitor) bool {
	if m.URL == "" || m.IntervalSeconds < 1 || m.TimeoutSeconds < 1 || m.FailureThreshold < 1 || m.RecoveryThreshold < 1 {
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

func responseError(resp *http.Response, resource bool) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return errors.New("Irisbeat rejected the API key as invalid, expired, or revoked")
	case http.StatusForbidden:
		return errors.New("the API key has insufficient permission. Use an Irisbeat Write API key")
	case http.StatusBadRequest:
		return errors.New("Irisbeat rejected the Monitor configuration. Check the target, schedule, timing, and installation check floor")
	case http.StatusConflict:
		return errors.New("Irisbeat reported a conflict. Check the installation before trying again")
	case http.StatusNotFound:
		mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		var payload struct {
			Error string `json:"error"`
		}
		if resource && mediaType == "application/json" && decode(resp.Body, &payload) && payload.Error == "Monitor not found" {
			return ErrMonitorMissing
		}
	}
	return fmt.Errorf("Irisbeat request failed (HTTP %d)", resp.StatusCode)
}
