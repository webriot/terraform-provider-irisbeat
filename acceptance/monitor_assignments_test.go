// SPDX-License-Identifier: AGPL-3.0-only
package acceptance_test

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMonitorAssignmentOwnership(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	operatorSecret := os.Getenv("IRISBEAT_TEST_INSTALLATION_SECRET")
	if baseURL == "" || key == "" || operatorSecret == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL, IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_INSTALLATION_SECRET for disposable Irisbeat")
	}
	for _, tc := range []struct{ kind, target string }{
		{"http", `url = "https://example.com/"`},
		{"tcp", "hostname = \"db.example.com\"\n  port = 5432"},
		{"heartbeat", ""},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			testMonitorAssignmentOwnership(t, baseURL, key, operatorSecret, tc.kind, tc.target)
		})
	}
}

func testMonitorAssignmentOwnership(t *testing.T, baseURL, key, operatorSecret, kind, target string) {
	t.Helper()
	w := workspace(t, baseURL, key)
	config := func(name, assignments string) {
		w.config(fmt.Sprintf(`
resource "irisbeat_notification_destination" "first" {
  channel     = "webhook"
  webhook_url = "https://hooks.example/first"
}

resource "irisbeat_notification_destination" "second" {
  channel     = "webhook"
  webhook_url = "https://hooks.example/second"
}

resource "irisbeat_monitor" "test" {
  name      = %q
  kind      = %q
  published = false
  %s
  %s
}
`, name, kind, target, assignments))
	}
	const first = "irisbeat_notification_destination.first.id"
	const second = "irisbeat_notification_destination.second.id"
	const both = "destination_ids = [" + first + ", " + second + "]"
	const reversed = "destination_ids = [" + second + ", " + first + "]"
	originalName := "Assignment acceptance " + rand.Text()
	const noColor = "-no-color"
	apply := func() { w.run(0, "apply", "-auto-approve", noColor) }
	noChanges := func() { w.run(0, "plan", "-detailed-exitcode", noColor) }

	config(originalName, both)
	apply()
	for _, destination := range []string{"first", "second"} {
		id := w.resourceState("irisbeat_notification_destination." + destination)["id"].(string)
		cleanupResource(t, baseURL+"/api/v1/destinations/"+id, key)
	}
	firstID := w.resourceState("irisbeat_notification_destination.first")["id"].(string)
	secondID := w.resourceState("irisbeat_notification_destination.second")["id"].(string)
	monitorID := w.monitor()["id"].(string)
	endpoint := baseURL + "/api/v1/monitors/" + monitorID
	cleanupResource(t, endpoint, key)

	saved := func(name string, ids ...string) {
		t.Helper()
		result := installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)
		assertFields(t, result, map[string]any{"id": monitorID, "name": name})
		assertRouting(t, result["destinationIds"], ids...)
	}
	operatorRouting := func(id string) {
		t.Helper()
		installationRequest(t, http.MethodPatch, baseURL+"/api/operator/monitors/"+monitorID, operatorSecret,
			fmt.Sprintf(`{"destinationIds":[%q]}`, id), http.StatusOK)
	}
	saved(originalName, firstID, secondID)
	assertRouting(t, w.monitor()["destination_ids"], firstID, secondID)
	config(originalName, reversed)
	noChanges()

	operatorRouting(secondID)
	w.run(0, "apply", "-refresh-only", "-auto-approve", noColor)
	assertRouting(t, w.monitor()["destination_ids"], secondID)
	saved(originalName, secondID)
	w.run(2, "plan", "-detailed-exitcode", noColor)
	apply()
	saved(originalName, firstID, secondID)
	noChanges()

	const unmanagedName = "Operator-owned routing"
	config(originalName, "")
	noChanges()
	saved(originalName, firstID, secondID)
	w.run(0, "state", "rm", address)
	w.run(0, "import", noColor, address, monitorID)
	assertRouting(t, w.monitor()["destination_ids"], firstID, secondID)
	config(unmanagedName, "")
	w.run(2, "plan", "-detailed-exitcode", noColor, "-out=rename.plan")
	operatorRouting(firstID)
	w.run(0, "apply", noColor, "rename.plan")
	saved(unmanagedName, firstID)

	operatorRouting(secondID)
	w.run(0, "apply", "-refresh-only", "-auto-approve", noColor)
	assertRouting(t, w.monitor()["destination_ids"], secondID)
	noChanges()
	saved(unmanagedName, secondID)

	config(unmanagedName, "destination_ids = []")
	apply()
	saved(unmanagedName)
	assertRouting(t, w.monitor()["destination_ids"])
	noChanges()

	config(unmanagedName, both)
	apply()
	saved(unmanagedName, firstID, secondID)
	config("Rejected rename", fmt.Sprintf("destination_ids = [%s, %q]", first, rand.Text()))
	w.run(1, "apply", "-auto-approve", noColor)
	saved(unmanagedName, firstID, secondID)
}

func TestUnknownAssignmentsBlockKindReplacement(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	secret := os.Getenv("IRISBEAT_TEST_INSTALLATION_SECRET")
	if baseURL == "" || key == "" || secret == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL, IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_INSTALLATION_SECRET for disposable Irisbeat")
	}
	w := workspace(t, baseURL, key)
	destinationID := installationRequest(t, http.MethodPost, baseURL+"/api/v1/destinations", key,
		`{"url":"https://hooks.example/kind-replacement"}`, http.StatusCreated)["id"].(string)
	cleanupResource(t, baseURL+"/api/v1/destinations/"+destinationID, key)
	w.config(fmt.Sprintf(`
resource "irisbeat_monitor" "test" {
  name            = "Assignment replacement validation"
  kind            = "heartbeat"
  published       = false
  destination_ids = [%q]
}
`, destinationID))
	w.run(0, "apply", "-auto-approve", "-no-color")
	id := w.monitor()["id"].(string)
	endpoint := baseURL + "/api/v1/monitors/" + id
	receiptPath := "/heartbeat/" + id
	cleanupResource(t, endpoint, key)
	installationRequest(t, http.MethodPost, baseURL+receiptPath, secret, "", http.StatusNoContent)
	saved := installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)
	receipt := func() any {
		t.Helper()
		snapshot := installationRequest(t, http.MethodGet, baseURL+"/api/v1/status", key, "", http.StatusOK)
		for _, entry := range snapshot["monitors"].([]any) {
			m := entry.(map[string]any)
			if m["id"] == id {
				return m["lastSeen"]
			}
		}
		t.Fatal("original Heartbeat is missing from Irisbeat status")
		return nil
	}
	lastSeen := receipt()
	if lastSeen == nil {
		t.Fatal("expected saved Heartbeat receipt history")
	}
	config := func(assignments string) {
		w.config(fmt.Sprintf(`
resource "terraform_data" "input" {
  input = { ids = [%q], id = %q }
}

resource "irisbeat_monitor" "test" {
  name            = "Assignment replacement validation"
  kind            = "http"
  url             = "https://example.com/"
  published       = false
  destination_ids = %s
}
`, destinationID, destinationID, assignments))
	}
	for _, tc := range []struct{ assignments, diagnostic string }{
		{fmt.Sprintf("[%q, null]", destinationID), "Null destination ID"},
		{"terraform_data.input.output.ids", "Monitor replacement requires a known value"},
		{fmt.Sprintf("[%q, terraform_data.input.output.id]", destinationID), "Monitor replacement requires a known value"},
	} {
		config(tc.assignments)
		out := w.run(1, "plan", "-no-color")
		if !strings.Contains(out, tc.diagnostic) || !strings.Contains(out, "destination_ids") {
			t.Fatalf("expected replacement diagnostic for destination_ids\n%s", out)
		}
		if got := installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK); !reflect.DeepEqual(got, saved) {
			t.Fatal("blocked replacement changed the saved Heartbeat or its routing")
		}
		if got := receipt(); got != lastSeen {
			t.Fatalf("lastSeen = %v, want %v", got, lastSeen)
		}
		assertFields(t, w.monitor(), map[string]any{"id": id, "heartbeat_path": receiptPath})
	}

	w.run(0, "apply", "-target=terraform_data.input", "-auto-approve", "-no-color")
	w.run(0, "apply", "-auto-approve", "-no-color")
	newID := w.monitor()["id"].(string)
	newEndpoint := baseURL + "/api/v1/monitors/" + newID
	cleanupResource(t, newEndpoint, key)
	if newID == id {
		t.Fatal("expected a new Monitor ID after changing kind")
	}
	installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusNotFound)
	replacement := installationRequest(t, http.MethodGet, newEndpoint, key, "", http.StatusOK)
	assertRouting(t, replacement["destinationIds"], destinationID)
	w.run(0, "plan", "-detailed-exitcode", "-no-color")
}

func assertRouting(t *testing.T, actual any, want ...string) {
	t.Helper()
	values, ok := actual.([]any)
	if !ok {
		t.Fatalf("expected a routing set, got %v", actual)
	}
	ids := make([]string, 0, len(values))
	for _, value := range values {
		id, ok := value.(string)
		if !ok {
			t.Fatalf("expected a destination ID, got %v", value)
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("routing = %v, want %v", ids, want)
	}
}

func TestMonitorRejectsPendingEmailAssignment(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	if baseURL == "" || key == "" || os.Getenv("IRISBEAT_TEST_EMAIL") != "1" {
		t.Skip("set IRISBEAT_TEST_EMAIL=1 for a disposable Irisbeat installation with local email delivery")
	}
	w := workspace(t, baseURL, key)
	webhook := installationRequest(t, http.MethodPost, baseURL+"/api/v1/destinations", key,
		`{"channel":"webhook","url":"https://hooks.example/current"}`, http.StatusCreated)
	webhookID := webhook["id"].(string)
	cleanupResource(t, baseURL+"/api/v1/destinations/"+webhookID, key)
	inbox := strings.ToLower(rand.Text()) + "@example.test"
	email := installationRequest(t, http.MethodPost, baseURL+"/api/v1/destinations", key,
		fmt.Sprintf(`{"channel":"email","address":%q}`, inbox), http.StatusCreated)
	emailID := email["id"].(string)
	cleanupResource(t, baseURL+"/api/v1/destinations/"+emailID, key)
	assertFields(t, email, map[string]any{"confirmed": false})

	config := func(label, name string, published bool, ids ...string) string {
		var quoted []string
		for _, id := range ids {
			quoted = append(quoted, fmt.Sprintf("%q", id))
		}
		return fmt.Sprintf(`
resource "irisbeat_monitor" %q {
  name            = %q
  kind            = "http"
  url             = "https://example.com/"
  published       = %t
  destination_ids = [%s]
}
`, label, name, published, strings.Join(quoted, ", "))
	}
	const noColor = "-no-color"
	name := "Pending email acceptance " + rand.Text()
	w.config(config("test", name, false, webhookID))
	w.run(0, "apply", "-auto-approve", noColor)
	id := w.monitor()["id"].(string)
	endpoint := baseURL + "/api/v1/monitors/" + id
	cleanupResource(t, endpoint, key)

	reject := func() {
		t.Helper()
		out := w.run(1, "apply", "-auto-approve", noColor)
		detail := strings.Join(strings.Fields(out), " ")
		if !strings.Contains(detail, "Ask the recipient to confirm the address, then reapply.") || strings.Contains(out, inbox) {
			t.Fatal("expected a redacted pending confirmation diagnostic")
		}
	}

	w.config(config("test", "Rejected pending email rename", true, webhookID, emailID))
	reject()
	saved := installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)
	assertFields(t, saved, map[string]any{"id": id, "name": name, "published": false})
	assertRouting(t, saved["destinationIds"], webhookID)

	rejectedCreateName := "Rejected pending email create " + rand.Text()
	w.config(config("test", name, false, webhookID) + config("rejected", rejectedCreateName, false, webhookID, emailID))
	reject()
	status := installationRequest(t, http.MethodGet, baseURL+"/api/v1/status", key, "", http.StatusOK)
	for _, monitor := range status["monitors"].([]any) {
		if monitor.(map[string]any)["name"] == rejectedCreateName {
			t.Fatal("rejected create left a saved Monitor")
		}
	}
}
