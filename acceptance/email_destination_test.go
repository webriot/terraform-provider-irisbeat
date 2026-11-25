// SPDX-License-Identifier: AGPL-3.0-only
package acceptance_test

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEmailDestinationLifecycle(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	if baseURL == "" || key == "" || os.Getenv("IRISBEAT_TEST_EMAIL") != "1" {
		t.Skip("set IRISBEAT_TEST_BASE_URL, IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_EMAIL=1 for disposable Irisbeat")
	}
	w := workspace(t, baseURL, key)
	local := "Acceptance-" + rand.Text()
	inbox := local + "@example.net"
	destinations := baseURL + "/api/v1/destinations"
	config := func(address, name string) {
		w.config(fmt.Sprintf(`
resource "irisbeat_notification_destination" "test" {
  channel       = "email"
  email_address = %q
  %s
}
`, address, name))
	}
	config("  "+local+"@EXAMPLE.NET  ", "")
	out := w.run(0, "apply", "-auto-approve", "-no-color")
	if strings.Contains(out, local) || !strings.Contains(out, "(sensitive value)") {
		t.Fatal("expected CLI output to hide the email address")
	}
	state := w.resourceState(destinationAddress)
	id := state["id"].(string)
	endpoint := destinations + "/" + id
	cleanupResource(t, endpoint, key)
	saved := installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)
	name := saved["name"].(string)
	if !strings.HasPrefix(name, "Email ") {
		t.Fatal("expected an Irisbeat-generated email name")
	}
	assertFields(t, saved, map[string]any{"channel": "email", "address": inbox, "confirmed": false})
	assertFields(t, state, map[string]any{"name": name, "confirmed": false, "webhook_url": nil})
	w.run(0, "plan", "-detailed-exitcode", "-no-color")

	w.run(0, "state", "rm", destinationAddress)
	w.run(0, "import", "-no-color", destinationAddress, id)
	assertFields(t, w.resourceState(destinationAddress), map[string]any{
		"id": id, "name": name, "email_address": inbox, "confirmed": false,
	})
	w.run(0, "plan", "-detailed-exitcode", "-no-color")

	renamed := "Renamed email " + rand.Text()
	config(local+"@EXAMPLE.NET", fmt.Sprintf("name = %q", renamed))
	out = w.run(2, "plan", "-detailed-exitcode", "-no-color")
	if !strings.Contains(out, "0 to add, 1 to change, 0 to destroy") {
		t.Fatal("expected an in-place email rename")
	}
	w.run(0, "apply", "-auto-approve", "-no-color")
	assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK),
		map[string]any{"id": id, "name": renamed, "address": inbox, "confirmed": false})
	w.run(0, "plan", "-detailed-exitcode", "-no-color")

	for _, invalid := range []string{local + "@", local + "@example..net", local + "@example.net,other@example.net"} {
		config(invalid, fmt.Sprintf("name = %q", renamed))
		out := w.run(1, "plan", "-detailed-exitcode", "-no-color")
		if !strings.Contains(out, "Invalid Irisbeat email address") || strings.Contains(out, local) {
			t.Fatal("expected a redacted email validation error")
		}
	}
	config(strings.ToLower(inbox), fmt.Sprintf("name = %q", renamed))
	expectDestinationReplacement(t, w, "email_address", local)
	installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK)

	nextInbox := "Replacement-" + rand.Text() + "@example.net"
	config(nextInbox, fmt.Sprintf("name = %q", renamed))
	expectDestinationReplacement(t, w, "email_address", nextInbox)
	w.run(0, "apply", "-no-color", "email-replacement.plan")
	installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusNotFound)
	newID := w.resourceState(destinationAddress)["id"].(string)
	newEndpoint := destinations + "/" + newID
	cleanupResource(t, newEndpoint, key)
	assertFields(t, installationRequest(t, http.MethodGet, newEndpoint, key, "", http.StatusOK),
		map[string]any{"name": renamed, "address": nextInbox, "confirmed": false})

	w.config(fmt.Sprintf(`
resource "irisbeat_notification_destination" "test" {
  channel     = "webhook"
  name        = %q
  webhook_url = "https://hooks.example/replacement"
}
`, renamed))
	expectDestinationReplacement(t, w, "channel", nextInbox)
	w.run(0, "apply", "-no-color", "email-replacement.plan")
	installationRequest(t, http.MethodGet, newEndpoint, key, "", http.StatusNotFound)
	hook := w.resourceState(destinationAddress)
	hookEndpoint := destinations + "/" + hook["id"].(string)
	cleanupResource(t, hookEndpoint, key)
	assertFields(t, hook, map[string]any{"channel": "webhook", "confirmed": nil, "email_address": nil})
	w.run(0, "plan", "-detailed-exitcode", "-no-color")

	w.run(0, "destroy", "-auto-approve", "-no-color")
	installationRequest(t, http.MethodGet, hookEndpoint, key, "", http.StatusNotFound)
}

func TestEmailDestinationConflictsHideAddress(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	if baseURL == "" || key == "" || os.Getenv("IRISBEAT_TEST_EMAIL") != "1" {
		t.Skip("set IRISBEAT_TEST_BASE_URL, IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_EMAIL=1 for disposable Irisbeat")
	}
	w := workspace(t, baseURL, key)
	local := "Email-conflict-" + rand.Text()
	name := "Email conflict " + rand.Text()
	destinations := baseURL + "/api/v1/destinations"
	original := installationRequest(t, http.MethodPost, destinations, key,
		fmt.Sprintf(`{"channel":"email","name":%q,"address":%q}`, name, local+"@example.net"), http.StatusCreated)
	cleanupResource(t, destinations+"/"+original["id"].(string), key)

	for _, conflict := range []struct{ name, local, detail string }{
		{strings.ToLower(name), local + "-different", "destination with this name already exists"},
		{name + " different", strings.ToLower(local), "inbox is already a destination"},
	} {
		w.config(fmt.Sprintf(`
resource "irisbeat_notification_destination" "test" {
  channel       = "email"
  name          = %q
  email_address = %q
}
`, conflict.name, conflict.local+"@EXAMPLE.NET"))
		out := w.run(1, "apply", "-auto-approve", "-no-color")
		detail := strings.Join(strings.Fields(out), " ")
		if !strings.Contains(detail, conflict.detail) || strings.Contains(strings.ToLower(out), strings.ToLower(local)) {
			t.Fatal("expected the CLI to identify the uniqueness conflict without exposing the email address")
		}
	}
}

func expectDestinationReplacement(t *testing.T, w cliWorkspace, field, sensitive string) {
	t.Helper()
	out := w.run(2, "plan", "-detailed-exitcode", "-no-color", "-out=email-replacement.plan")
	if strings.Contains(strings.ToLower(out), strings.ToLower(sensitive)) {
		t.Fatal("replacement plan exposed an email address")
	}
	assertDestinationReplacement(t, w, "email-replacement.plan", field)
}

func TestEmailDestinationCutover(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	if baseURL == "" || key == "" || os.Getenv("IRISBEAT_TEST_EMAIL") != "1" {
		t.Skip("set IRISBEAT_TEST_BASE_URL, IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_EMAIL=1 for disposable Irisbeat")
	}
	captureURL := os.Getenv("IRISBEAT_TEST_MAIL_CAPTURE_URL")
	if captureURL == "" {
		t.Fatal("set IRISBEAT_TEST_MAIL_CAPTURE_URL to the disposable Local Explorer email capture endpoint")
	}
	w := workspace(t, baseURL, key)
	suffix := rand.Text()
	oldInbox, newInbox := "old-"+suffix+"@example.net", "new-"+suffix+"@example.net"
	oldName, newName := "Original "+suffix, "Replacement "+suffix
	destinations := baseURL + "/api/v1/destinations"
	const original = "irisbeat_notification_destination.original"
	const replacement = "irisbeat_notification_destination.replacement"
	email := func(label, name, inbox string) string {
		return fmt.Sprintf(`
resource "irisbeat_notification_destination" "%s" {
  name          = %q
  channel       = "email"
  email_address = %q
}
`, label, name, inbox)
	}
	monitor := func(destination, name string, published bool) string {
		return fmt.Sprintf(`
resource "irisbeat_monitor" "test" {
  name            = %q
  kind            = "heartbeat"
  published       = %t
  destination_ids = [%s.id]
}
`, name, published, destination)
	}
	const originalMonitorName = "Original routing"
	const updatedMonitorName = "Updated routing"
	oldEmail := email("original", oldName, oldInbox)
	newEmail := email("replacement", newName, newInbox)
	oldRoute := monitor(original, originalMonitorName, false)
	newRoute := monitor(replacement, updatedMonitorName, true)
	const apply = "-auto-approve"
	const noColor = "-no-color"

	w.config(oldEmail)
	w.run(0, "apply", apply, noColor)
	oldID := w.resourceState(original)["id"].(string)
	oldEndpoint := destinations + "/" + oldID
	cleanupResource(t, oldEndpoint, key)
	confirmCapturedEmail(t, baseURL, captureURL, oldInbox)
	w.config(oldEmail + oldRoute)
	w.run(0, "apply", apply, noColor)
	monitorID := w.monitor()["id"].(string)
	monitorEndpoint := baseURL + "/api/v1/monitors/" + monitorID
	cleanupResource(t, monitorEndpoint, key)
	assertAssignment(t, monitorEndpoint, key, oldID)

	w.config(`
resource "irisbeat_notification_destination" "original" {
  channel = "webhook"
}
` + oldRoute)
	if out := w.run(1, "plan", "-detailed-exitcode", noColor); !strings.Contains(out, "Invalid Irisbeat webhook URL") {
		t.Fatal("expected planning to reject the missing webhook URL")
	}
	assertComputedChannelChangeBlocked(t, w, "original", "webhook", "webhook_url", "Invalid Irisbeat webhook URL", oldRoute)
	assertFields(t, w.resourceState(original), map[string]any{"id": oldID, "channel": "email"})
	assertFields(t, installationRequest(t, http.MethodGet, oldEndpoint, key, "", http.StatusOK),
		map[string]any{"id": oldID, "channel": "email", "confirmed": true})
	assertAssignment(t, monitorEndpoint, key, oldID)

	w.config(oldEmail + newEmail + newRoute)
	out := w.run(1, "apply", apply, noColor)
	detail := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(detail, "Ask the recipient to confirm the address, then reapply.") ||
		strings.Contains(out, oldInbox) || strings.Contains(out, newInbox) {
		t.Fatal("expected redacted recipient confirmation instructions")
	}
	newState := w.resourceState(replacement)
	newID := newState["id"].(string)
	newEndpoint := destinations + "/" + newID
	cleanupResource(t, newEndpoint, key)
	assertFields(t, newState, map[string]any{"name": newName, "confirmed": false})
	assertFields(t, installationRequest(t, http.MethodGet, newEndpoint, key, "", http.StatusOK),
		map[string]any{"id": newID, "address": newInbox, "confirmed": false})

	saved := installationRequest(t, http.MethodGet, monitorEndpoint, key, "", http.StatusOK)
	assertFields(t, saved, map[string]any{"id": monitorID, "name": originalMonitorName, "published": false})
	assertRouting(t, saved["destinationIds"], oldID)
	assertFields(t, w.monitor(), map[string]any{"id": monitorID, "name": originalMonitorName, "published": false})
	assertRouting(t, w.monitor()["destination_ids"], oldID)

	// The staged configuration keeps both destinations and the old routing while confirmation is pending.
	w.config(oldEmail + newEmail + oldRoute)
	w.run(0, "plan", "-detailed-exitcode", noColor)
	confirmCapturedEmail(t, baseURL, captureURL, newInbox)
	assertAssignment(t, monitorEndpoint, key, oldID)

	w.config(oldEmail + newEmail + newRoute)
	w.run(0, "apply", "-refresh-only", apply, noColor)
	assertFields(t, w.resourceState(replacement), map[string]any{"id": newID, "confirmed": true})
	assertAssignment(t, monitorEndpoint, key, oldID)
	w.run(0, "apply", apply, noColor)
	saved = installationRequest(t, http.MethodGet, monitorEndpoint, key, "", http.StatusOK)
	assertFields(t, saved, map[string]any{"id": monitorID, "name": updatedMonitorName, "published": true})
	assertRouting(t, saved["destinationIds"], newID)
	assertFields(t, installationRequest(t, http.MethodGet, oldEndpoint, key, "", http.StatusOK),
		map[string]any{"id": oldID, "confirmed": true})
	assertFields(t, w.resourceState(replacement), map[string]any{"id": newID, "confirmed": true})

	w.config(newEmail + newRoute)
	w.run(0, "apply", apply, noColor)
	installationRequest(t, http.MethodGet, oldEndpoint, key, "", http.StatusNotFound)
	assertAssignment(t, monitorEndpoint, key, newID)
	w.run(0, "plan", "-detailed-exitcode", noColor)
	w.run(0, "destroy", apply, noColor)
	installationRequest(t, http.MethodGet, newEndpoint, key, "", http.StatusNotFound)
}

func confirmCapturedEmail(t *testing.T, baseURL, captureURL, inbox string) {
	t.Helper()
	capture, err := url.Parse(captureURL)
	if err != nil {
		t.Fatal("invalid mail capture URL")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	readCapture := func(endpoint string, result any) {
		t.Helper()
		resp, err := client.Get(endpoint)
		if err != nil {
			t.Fatal("could not reach the mail capture endpoint")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(result) != nil {
			t.Fatal("invalid mail capture response")
		}
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var messages struct {
			Result []struct {
				MessageID string
				Subject   string
				To        []string
			}
		}
		readCapture(capture.String(), &messages)
		for _, message := range messages.Result {
			if message.Subject != "Confirm Irisbeat email notifications" ||
				len(message.To) != 1 || message.To[0] != inbox {
				continue
			}
			query := capture.Query()
			query.Set("email_id", message.MessageID)
			detail := *capture
			detail.RawQuery = query.Encode()
			var mail struct{ Result struct{ Text string } }
			readCapture(detail.String(), &mail)
			for _, field := range strings.Fields(mail.Result.Text) {
				link, err := url.Parse(field)
				if err != nil || link.Scheme+"://"+link.Host != baseURL ||
					link.Path != "/notifications/confirm" || link.Fragment == "" {
					continue
				}
				token := link.Fragment
				link.Fragment = ""
				// Submit the same token as the recipient page's form on its bound origin.
				resp, err := client.PostForm(link.String(), url.Values{"token": {token}})
				if err != nil {
					t.Fatal("could not submit the captured confirmation")
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("recipient confirmation returned HTTP %d", resp.StatusCode)
				}
				return
			}
			t.Fatal("captured email did not contain a confirmation link for the disposable installation")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the recipient confirmation email")
}
