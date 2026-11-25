// SPDX-License-Identifier: AGPL-3.0-only
package acceptance_test

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

const destinationAddress = "irisbeat_notification_destination.test"

func TestWebhookDestinationLifecycle(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	operatorSecret := os.Getenv("IRISBEAT_TEST_INSTALLATION_SECRET")
	if baseURL == "" || key == "" || operatorSecret == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_INSTALLATION_SECRET for disposable Irisbeat")
	}
	w := workspace(t, baseURL, key)
	const original = "https://discord.com/api/webhooks/123/acceptance-token"
	const canonical = original + "/slack"
	config := func(url, name string) {
		w.config(fmt.Sprintf(`
resource "irisbeat_notification_destination" "test" {
  channel     = "webhook"
  webhook_url = %q
  %s
}
`, url, name))
	}
	config(original, "")
	out := w.run(0, "apply", "-auto-approve", "-no-color")
	if strings.Contains(out, "acceptance-token") || !strings.Contains(out, "(sensitive value)") {
		t.Fatal("expected CLI output to hide the webhook URL")
	}
	state := w.resourceState(destinationAddress)
	id := state["id"].(string)
	endpoint := baseURL + "/api/v1/destinations/" + id
	cleanupResource(t, endpoint, key)
	saved := installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)
	if name, ok := saved["name"].(string); !ok || !strings.HasPrefix(name, "Webhook ") || state["name"] != name {
		t.Fatal("expected an Irisbeat-generated name in state")
	}
	assertFields(t, saved, map[string]any{"channel": "webhook", "url": canonical})
	w.run(0, "plan", "-detailed-exitcode", "-no-color")
	w.run(0, "state", "rm", destinationAddress)
	w.run(0, "import", "-no-color", destinationAddress, id)
	assertFields(t, w.resourceState(destinationAddress), map[string]any{"id": id, "name": saved["name"], "webhook_url": canonical})
	config("https://DISCORD.com:443/api/webhooks/123/acceptance-token/", "")
	w.run(0, "plan", "-detailed-exitcode", "-no-color")

	installationRequest(t, http.MethodPut, baseURL+"/api/operator/destinations/"+id, operatorSecret,
		`{"name":"Operator webhook"}`, http.StatusOK)
	w.run(0, "apply", "-refresh-only", "-auto-approve", "-no-color")
	assertFields(t, w.resourceState(destinationAddress), map[string]any{"id": id, "name": "Operator webhook"})

	managedName := "Managed webhook " + rand.Text()
	config(original, fmt.Sprintf("name = %q", "  "+managedName+"  "))
	out = w.run(2, "plan", "-detailed-exitcode", "-no-color")
	if !strings.Contains(out, "0 to add, 1 to change, 0 to destroy") {
		t.Fatal("expected an in-place rename")
	}
	w.run(0, "apply", "-auto-approve", "-no-color")
	assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK), map[string]any{
		"id": id, "name": managedName, "url": canonical,
	})
	w.run(0, "plan", "-detailed-exitcode", "-no-color")

	monitorEndpoint := assignedMonitor(t, baseURL, key, id)
	w.config(`
resource "irisbeat_notification_destination" "test" {
  channel = "email"
}
`)
	if out := w.run(1, "plan", "-detailed-exitcode", "-no-color"); !strings.Contains(out, "Invalid Irisbeat email address") {
		t.Fatal("expected planning to reject the missing email address")
	}
	assertFields(t, w.resourceState(destinationAddress), map[string]any{"id": id, "channel": "webhook"})
	installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)
	assertAssignment(t, monitorEndpoint, key, id)

	assertComputedChannelChangeBlocked(t, w, "test", "email", "email_address", "Invalid Irisbeat email address", "")
	assertFields(t, w.resourceState(destinationAddress), map[string]any{"id": id, "channel": "webhook"})
	assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK),
		map[string]any{"id": id, "channel": "webhook", "url": canonical})
	assertAssignment(t, monitorEndpoint, key, id)

	for _, url := range []string{
		strings.Replace(original, "https:", "http:", 1),
		"https://[bad]/acceptance-token",
		"https://acceptance-token@hooks.example/",
		original + "#acceptance-token",
		original + "\u00a0",
		original + strings.Repeat("😀", (2048-len(original))/2+1),
	} {
		installationRequest(t, http.MethodPost, baseURL+"/api/v1/destinations", key,
			fmt.Sprintf(`{"channel":"webhook","url":%q}`, url), http.StatusBadRequest)
		config(url, fmt.Sprintf("name = %q", managedName))
		out = w.run(1, "plan", "-detailed-exitcode", "-no-color")
		if !strings.Contains(out, "Invalid Irisbeat webhook URL") || strings.Contains(out, "acceptance-token") {
			t.Fatal("expected a redacted webhook validation error")
		}
	}
	installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)
	assertAssignment(t, monitorEndpoint, key, id)

	const replacement = "https://hooks.example/new-acceptance-token"
	config(replacement, fmt.Sprintf("name = %q", managedName))
	out = w.run(2, "plan", "-detailed-exitcode", "-no-color", "-out=replace.plan")
	if strings.Contains(out, "acceptance-token") || !strings.Contains(out, "1 to add, 0 to change, 1 to destroy") {
		t.Fatal("expected a redacted replacement plan")
	}
	assertDestinationReplacement(t, w, "replace.plan", "webhook_url")
	w.run(0, "apply", "-no-color", "replace.plan")
	installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusNotFound)
	replacementID := w.resourceState(destinationAddress)["id"].(string)
	if replacementID == id {
		t.Fatal("expected the replacement to have a new ID")
	}
	replacementEndpoint := baseURL + "/api/v1/destinations/" + replacementID
	cleanupResource(t, replacementEndpoint, key)
	assertFields(t, installationRequest(t, http.MethodGet, replacementEndpoint, key, "", http.StatusOK),
		map[string]any{"name": managedName, "url": replacement})
	w.run(0, "plan", "-detailed-exitcode", "-no-color")
	w.run(0, "destroy", "-auto-approve", "-no-color")
	installationRequest(t, http.MethodGet, replacementEndpoint, key, "", http.StatusNotFound)
	out = w.run(1, "import", "-no-color", destinationAddress, replacementID)
	if !strings.Contains(out, "does not exist in this Irisbeat installation") {
		t.Fatal("expected a missing destination import diagnostic")
	}

	w.run(0, "apply", "-auto-approve", "-no-color")
	externalID := w.resourceState(destinationAddress)["id"].(string)
	externalEndpoint := baseURL + "/api/v1/destinations/" + externalID
	cleanupResource(t, externalEndpoint, key)
	installationRequest(t, http.MethodDelete, externalEndpoint, key, "", http.StatusNoContent)
	w.run(0, "apply", "-refresh-only", "-auto-approve", "-no-color")
	if out := w.run(0, "state", "list"); strings.Contains(out, destinationAddress) {
		t.Fatal("refresh should remove state for a deleted destination")
	}
}

func assertComputedChannelChangeBlocked(t *testing.T, w cliWorkspace, resourceName, channel, attribute, invalidDiagnostic, otherConfig string) {
	t.Helper()
	const invalid = "secret-invalid-destination-address"
	w.config(fmt.Sprintf(`
resource "terraform_data" "next_address" {
  input = "selected"
}

resource "irisbeat_notification_destination" "%s" {
  channel = %q
  %s = { selected = %q }[terraform_data.next_address.output]
}
`, resourceName, channel, attribute, invalid) + otherConfig)
	out := w.run(1, "plan", "-detailed-exitcode", "-no-color")
	detail := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(detail, "Notification channel change requires known addresses") ||
		!strings.Contains(detail, "Apply the upstream dependencies separately, then run a fresh plan") ||
		strings.Contains(out, invalid) || strings.Contains(out, "must be replaced") {
		t.Fatal("expected a redacted plan error with staging guidance before replacement")
	}

	w.run(0, "apply", "-auto-approve", "-no-color", "-target=terraform_data.next_address")
	out = w.run(1, "plan", "-detailed-exitcode", "-no-color")
	if !strings.Contains(out, invalidDiagnostic) || strings.Contains(out, invalid) || strings.Contains(out, "must be replaced") {
		t.Fatal("expected planning to reject the resolved invalid address without displaying it")
	}
	w.run(0, "state", "rm", "terraform_data.next_address")
}

func assignedMonitor(t *testing.T, baseURL, key, destinationID string) string {
	t.Helper()
	saved := installationRequest(t, http.MethodPost, baseURL+"/api/v1/monitors", key,
		fmt.Sprintf(`{"name":%q,"kind":"heartbeat","published":false,"destinationIds":[%q]}`,
			"Routing check "+rand.Text(), destinationID), http.StatusCreated)
	endpoint := baseURL + "/api/v1/monitors/" + saved["id"].(string)
	cleanupResource(t, endpoint, key)
	assertAssignment(t, endpoint, key, destinationID)
	return endpoint
}

func assertAssignment(t *testing.T, endpoint, key, destinationID string) {
	t.Helper()
	saved := installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)
	ids, ok := saved["destinationIds"].([]any)
	if !ok || len(ids) != 1 || ids[0] != destinationID {
		t.Fatal("expected Monitor assignment to the original destination")
	}
}

func TestComputedWebhookURL(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	if baseURL == "" || key == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL and IRISBEAT_TEST_API_KEY for disposable Irisbeat")
	}
	w := workspace(t, baseURL, key)
	config := func(url string, generation int) {
		// Keep URLs out of the built-in resource's own plan output.
		w.config(fmt.Sprintf(`
resource "terraform_data" "hook_config" {
  input = { key = "hook", generation = %d }
}

resource "irisbeat_notification_destination" "test" {
  channel     = "webhook"
  webhook_url = { hook = %q }[terraform_data.hook_config.output.key]
}
`, generation, url))
	}
	const original = "https://discord.com/api/webhooks/123/computed-webhook-token"
	const equivalent = original + "/slack"
	config(original, 1)
	w.run(0, "apply", "-auto-approve", "-no-color")
	id := w.resourceState(destinationAddress)["id"].(string)
	endpoint := baseURL + "/api/v1/destinations/" + id
	cleanupResource(t, endpoint, key)
	monitorEndpoint := assignedMonitor(t, baseURL, key, id)

	for generation, url := range []string{original, equivalent} {
		config(url, generation+2)
		out := w.run(2, "plan", "-detailed-exitcode", "-no-color", "-out=equivalent.plan")
		if !strings.Contains(out, "0 to destroy") || !strings.Contains(out, "Webhook URL is unknown") ||
			strings.Contains(out, "computed-webhook-token") {
			t.Fatal("expected redacted staging guidance and no replacement while the URL is unknown")
		}
		w.run(0, "apply", "-no-color", "equivalent.plan")
		assertFields(t, w.resourceState(destinationAddress), map[string]any{"id": id})
		assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK),
			map[string]any{"id": id, "url": equivalent})
		assertAssignment(t, monitorEndpoint, key, id)
		w.run(0, "plan", "-detailed-exitcode", "-no-color")
	}

	const changed = "https://hooks.example/changed-webhook-token"
	config(changed, 4)
	out := w.run(2, "plan", "-detailed-exitcode", "-no-color", "-out=changed.plan")
	if !strings.Contains(out, "0 to destroy") || !strings.Contains(out, "Webhook URL is unknown") ||
		strings.Contains(out, "changed-webhook-token") {
		t.Fatal("expected redacted staging guidance and no replacement while the URL is unknown")
	}
	out = w.run(1, "apply", "-no-color", "changed.plan")
	if !strings.Contains(out, "inconsistent final plan") || !strings.Contains(out, "Update") ||
		!strings.Contains(strings.Join(strings.Fields(out), " "), "run a fresh plan") ||
		strings.Contains(out, "computed-webhook-token") || strings.Contains(out, "changed-webhook-token") {
		t.Fatal("expected the CLI to reject a late replacement without exposing webhook URLs")
	}
	assertFields(t, w.resourceState(destinationAddress), map[string]any{"id": id})
	installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)
	assertAssignment(t, monitorEndpoint, key, id)

	out = w.run(2, "plan", "-detailed-exitcode", "-no-color", "-out=replace-computed.plan")
	if !strings.Contains(out, "1 to add, 0 to change, 1 to destroy") ||
		strings.Contains(out, "changed-webhook-token") {
		t.Fatal("expected the next plan to show native replacement for the resolved URL")
	}
	w.run(0, "apply", "-no-color", "replace-computed.plan")
	newID := w.resourceState(destinationAddress)["id"].(string)
	newEndpoint := baseURL + "/api/v1/destinations/" + newID
	cleanupResource(t, newEndpoint, key)
	if newID == id {
		t.Fatal("expected the reviewed replacement to have a new ID")
	}
	installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusNotFound)
	assertFields(t, installationRequest(t, http.MethodGet, newEndpoint, key, "", http.StatusOK),
		map[string]any{"id": newID, "url": changed})
	w.run(0, "plan", "-detailed-exitcode", "-no-color")
}

func TestWebhookDestinationConflictHidesURL(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	if baseURL == "" || key == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL and IRISBEAT_TEST_API_KEY for disposable Irisbeat")
	}
	w := workspace(t, baseURL, key)
	name := "Conflict " + rand.Text()
	destination := installationRequest(t, http.MethodPost, baseURL+"/api/v1/destinations", key,
		fmt.Sprintf(`{"name":%q,"url":"https://hooks.example/first"}`, name), http.StatusCreated)
	cleanupResource(t, baseURL+"/api/v1/destinations/"+destination["id"].(string), key)
	w.config(fmt.Sprintf(`
resource "irisbeat_notification_destination" "test" {
  channel     = "webhook"
  name        = %q
  webhook_url = "https://hooks.example/webhook-conflict-token"
}
`, strings.ToLower(name)))
	out := w.run(1, "apply", "-auto-approve", "-no-color")
	if !strings.Contains(out, "reported a conflict") || strings.Contains(out, "webhook-conflict-token") {
		t.Fatal("expected a redacted destination conflict diagnostic")
	}
}

func assertDestinationReplacement(t *testing.T, w cliWorkspace, planFile, field string) {
	t.Helper()
	var plan struct {
		Changes []struct {
			Address string
			Change  struct {
				Actions      []string
				ReplacePaths [][]string      `json:"replace_paths"`
				AfterUnknown map[string]bool `json:"after_unknown"`
			}
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal([]byte(w.run(0, "show", "-json", planFile)), &plan); err != nil {
		t.Fatal("could not decode replacement plan")
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Address != destinationAddress {
		t.Fatal("expected one destination change")
	}
	change := plan.Changes[0].Change
	if strings.Join(change.Actions, ",") != "delete,create" || len(change.ReplacePaths) != 1 ||
		strings.Join(change.ReplacePaths[0], ".") != field || !change.AfterUnknown["id"] {
		t.Fatalf("expected %s to force replacement with a new ID", field)
	}
}
