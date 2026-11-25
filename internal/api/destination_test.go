// SPDX-License-Identifier: AGPL-3.0-only
package api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Webriot/terraform-provider-irisbeat/internal/api"
)

func TestCreateDestinationReplaysLostResponse(t *testing.T) {
	const webhook = "https://hooks.example/private-token"
	requests := make(chan [2]string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/destinations" || r.Header.Get("Authorization") != "Bearer write-key" {
			t.Error("unexpected destination create request")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		first := len(requests) == 0
		requests <- [2]string{r.Header.Get("Idempotency-Key"), string(body)}
		if first {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"destination-id","name":"Webhook 1","channel":"webhook","url":"`+webhook+`"}`)
	}))
	defer server.Close()

	client, err := api.New(server.URL, "write-key")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := client.CreateDestination(context.Background(), map[string]any{"channel": "webhook", "url": webhook})
	if err != nil || destination.ID != "destination-id" {
		t.Fatalf("destination create returned ID %q and error %v", destination.ID, err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	first, second := <-requests, <-requests
	if first[0] == "" || first != second || !strings.Contains(first[1], webhook) {
		t.Fatal("expected retries to use the same key and exact request bytes")
	}
}

func TestOnlyDestinationMissingResponseClassifiesAsMissing(t *testing.T) {
	for _, tc := range []struct {
		body    string
		missing bool
	}{
		{`{"error":"Destination not found"}`, true},
		{`{"error":"Monitor not found"}`, false},
		{`{"error":"https://hooks.example/private-token"}`, false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.EscapedPath() != "/api/v1/destinations/opaque%2Fid" {
				t.Error("destination ID must remain one escaped path segment")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, tc.body)
		}))
		client, err := api.New(server.URL, "write-key")
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		_, readErr := client.ReadDestination(context.Background(), "opaque/id")
		deleteErr := client.DeleteDestination(context.Background(), "opaque/id")
		server.Close()
		for _, err := range []error{readErr, deleteErr} {
			if err == nil || errors.Is(err, api.ErrDestinationMissing) != tc.missing || strings.Contains(err.Error(), "private-token") {
				t.Fatal("expected a safe, classified destination error")
			}
		}
	}
}
