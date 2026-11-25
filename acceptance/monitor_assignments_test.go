// SPDX-License-Identifier: AGPL-3.0-only
package acceptance_test

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
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
  kind      = "http"
  url       = "https://example.com/"
  published = false
  %s
}
`, name, assignments))
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
