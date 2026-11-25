// SPDX-License-Identifier: AGPL-3.0-only
package acceptance_test

import (
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestInvalidKindReplacementPreservesMonitor(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	secret := os.Getenv("IRISBEAT_TEST_INSTALLATION_SECRET")
	if baseURL == "" || key == "" || secret == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL, IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_INSTALLATION_SECRET for disposable Irisbeat")
	}
	for _, tc := range []struct {
		name, originalKind, originalTarget, replacementKind, input, field, invalid string
	}{
		{"HTTP to Heartbeat", "http", `url = "https://example.com/"`, "heartbeat",
			`"https://example.com/"`, "url", "HTTP kind required"},
		{"Heartbeat to HTTP", "heartbeat", "", "http",
			"tostring(null)", "url", "Missing HTTP target"},
		{"TCP to Heartbeat", "tcp", `hostname = "db.example.com"
port = 5432`, "heartbeat", "5432", "port", "TCP kind required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := workspace(t, baseURL, key)
			config := func(kind, target, producer string) {
				w.config(fmt.Sprintf(`
%s
resource "irisbeat_monitor" "test" {
  name = "Replacement validation"
  kind = %q
  published = false
  %s
}
`, producer, kind, target))
			}
			config(tc.originalKind, tc.originalTarget, "")
			w.run(0, "apply", "-auto-approve", "-no-color")
			monitorID := w.monitor()["id"].(string)
			endpoint := baseURL + "/api/v1/monitors/" + monitorID
			t.Cleanup(func() {
				installationRequest(t, http.MethodDelete, endpoint, key, "", http.StatusNoContent)
			})
			destinationID := installationRequest(t, http.MethodPost, baseURL+"/api/v1/destinations", key,
				`{"url":"https://example.com/irisbeat-acceptance","name":"Replacement validation"}`, http.StatusCreated)["id"].(string)
			t.Cleanup(func() {
				installationRequest(t, http.MethodDelete, baseURL+"/api/v1/destinations/"+destinationID, key, "", http.StatusNoContent)
			})
			installationRequest(t, http.MethodPatch, endpoint, key,
				fmt.Sprintf(`{"destinationIds":[%q]}`, destinationID), http.StatusOK)
			if tc.originalKind == "heartbeat" {
				installationRequest(t, http.MethodPost, baseURL+"/heartbeat/"+monitorID, secret, "", http.StatusNoContent)
			}
			saved := installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)
			status := func() map[string]any {
				t.Helper()
				snapshot := installationRequest(t, http.MethodGet, baseURL+"/api/v1/status", key, "", http.StatusOK)
				for _, entry := range snapshot["monitors"].([]any) {
					m := entry.(map[string]any)
					if m["id"] == monitorID {
						return m
					}
				}
				t.Fatal("original Monitor is missing from Irisbeat status")
				return nil
			}
			before := status()
			if tc.originalKind == "heartbeat" && before["lastSeen"] == nil {
				t.Fatal("expected saved Heartbeat receipt history")
			}

			config(tc.replacementKind, tc.field+" = terraform_data.input.output.value", fmt.Sprintf(`
resource "terraform_data" "input" {
  input = { value = %s }
}
`, tc.input))
			out := w.run(1, "plan", "-detailed-exitcode", "-no-color", "-out=replacement.tfplan")
			if !strings.Contains(out, "Monitor replacement requires a known value") || !strings.Contains(out, tc.field) {
				t.Fatalf("expected replacement diagnostic for %s\n%s", tc.field, out)
			}

			// Once the input is known, normal validation diagnoses the invalid configuration.
			config(tc.replacementKind, tc.field+" = "+tc.input, "")
			out = w.run(1, "apply", "-auto-approve", "-no-color")
			if !strings.Contains(out, tc.invalid) {
				t.Fatalf("expected %q after resolving the input\n%s", tc.invalid, out)
			}
			if got := installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK); !reflect.DeepEqual(got, saved) {
				t.Fatal("invalid replacement changed saved configuration or destination assignments")
			}
			after := status()
			if !reflect.DeepEqual(after["lastSeen"], before["lastSeen"]) || !reflect.DeepEqual(after["lastResult"], before["lastResult"]) {
				t.Fatal("invalid replacement changed Monitor history")
			}
			assertFields(t, w.monitor(), map[string]any{"id": monitorID, "kind": tc.originalKind})
		})
	}
}
