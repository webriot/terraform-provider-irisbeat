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
	const replacement = "https://hooks.example/new-acceptance-token"
	config(replacement, fmt.Sprintf("name = %q", managedName))
	out = w.run(2, "plan", "-detailed-exitcode", "-no-color", "-out=replace.plan")
	if strings.Contains(out, "acceptance-token") || !strings.Contains(out, "1 to add, 0 to change, 1 to destroy") {
		t.Fatal("expected a redacted replacement plan")
	}
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
	if err := json.Unmarshal([]byte(w.run(0, "show", "-json", "replace.plan")), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Address != destinationAddress {
		t.Fatal("expected one destination change")
	}
	change := plan.Changes[0].Change
	if strings.Join(change.Actions, ",") != "delete,create" || len(change.ReplacePaths) != 1 ||
		strings.Join(change.ReplacePaths[0], ".") != "webhook_url" || !change.AfterUnknown["id"] {
		t.Fatal("expected webhook_url to force replacement with a new ID")
	}
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
