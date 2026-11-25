// SPDX-License-Identifier: AGPL-3.0-only
package api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Webriot/terraform-provider-irisbeat/internal/api"
)

const savedMonitor = `{"id":"monitor-id","name":"Home","kind":"http","url":"https://example.com/","published":false,"scheduleMode":"interval","intervalSeconds":60,"timeoutSeconds":10,"failureThreshold":2,"recoveryThreshold":2,"destinationIds":[]}`

func TestCreateReplaysLostResponse(t *testing.T) {
	requests := make(chan [2]string, 4)
	var savedKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Header.Get("Idempotency-Key")
		requests <- [2]string{key, string(body)}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/monitors" || r.Header.Get("Authorization") != "Bearer write-key" {
			t.Error("unexpected create request")
		}
		if savedKey == "" {
			savedKey = key
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		if key != savedKey {
			t.Error("retry would create a second Monitor")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, savedMonitor)
	}))
	defer server.Close()

	client, err := api.New(server.URL, "write-key")
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := client.CreateMonitor(context.Background(), map[string]any{"name": "Home"})
	if err != nil || monitor.ID != "monitor-id" {
		t.Fatalf("create = %v, %v", monitor.ID, err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	first, second := <-requests, <-requests
	if first[0] == "" || !strings.Contains(first[1], `"name":"Home"`) || first != second {
		t.Fatal("expected the same nonempty key and serialized body on both requests")
	}
}

func TestCreateRejectsWrongResourceResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, strings.Replace(savedMonitor, `"kind":"http"`, `"kind":"tcp"`, 1))
	}))
	defer server.Close()
	client, err := api.New(server.URL, "write-key")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateMonitor(context.Background(), map[string]any{"kind": "http"})
	if err == nil || !strings.Contains(err.Error(), "import any existing Monitor") {
		t.Fatalf("create returned %v, want recovery instructions", err)
	}
}

func TestCreateRetryTermination(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status, attempts int
		message          string
	}{
		{"transient", http.StatusServiceUnavailable, 3, "import any existing Monitor"},
		{"unauthorized", http.StatusUnauthorized, 1, "invalid, expired, or revoked"},
		{"forbidden", http.StatusForbidden, 1, "insufficient permission"},
		{"conflict", http.StatusConflict, 1, "reported a conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":"response-secret"}`)
			}))
			defer server.Close()
			client, err := api.New(server.URL, "request-secret")
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.CreateMonitor(context.Background(), map[string]any{"name": "body-secret"})
			if err == nil || !strings.Contains(err.Error(), tc.message) || strings.Contains(err.Error(), "-secret") {
				t.Fatal("expected a safe, classified create error")
			}
			if int(attempts.Load()) != tc.attempts {
				t.Fatalf("attempts = %d, want %d", attempts.Load(), tc.attempts)
			}
		})
	}
}

func TestOnlyMonitorMissingResponseClassifiesAsMissing(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
		missing                 bool
	}{
		{"specific", "application/json; charset=utf-8", `{"error":"Monitor not found"}`, 404, true},
		{"generic JSON", "application/json", `{"error":"Not Found"}`, 404, false},
		{"non-JSON media type", "text/plain", `{"error":"Monitor not found"}`, 404, false},
		{"status mismatch", "application/json", `{"error":"Monitor not found"}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != "/api/v1/monitors/opaque%2Fid%3F%23" {
					t.Error("Monitor ID must remain one escaped path segment")
				}
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, err := api.New(server.URL, "write-key")
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := client.ReadMonitor(context.Background(), "opaque/id?#")
			deleteErr := client.DeleteMonitor(context.Background(), "opaque/id?#")
			for _, err := range []error{readErr, deleteErr} {
				if err == nil || errors.Is(err, api.ErrMonitorMissing) != tc.missing {
					t.Fatalf("unexpected error classification: %v", err)
				}
			}
		})
	}
}
