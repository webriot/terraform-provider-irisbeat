// SPDX-License-Identifier: AGPL-3.0-only
package acceptance_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestTCPMonitorLifecycle(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	operatorSecret := os.Getenv("IRISBEAT_TEST_INSTALLATION_SECRET")
	if baseURL == "" || key == "" || operatorSecret == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL, IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_INSTALLATION_SECRET for disposable Irisbeat with a 120-second check floor")
	}
	w := workspace(t, baseURL, key)
	const auto = "-auto-approve"
	const noColor = "-no-color"
	config := func(hostname string, port int, schedule string) {
		w.config(fmt.Sprintf(`
resource "irisbeat_monitor" "test" {
  name      = "TCP acceptance"
  kind      = "tcp"
  hostname  = %q
  port      = %d
  published = false
  %s
}
`, hostname, port, schedule))
	}
	config("DB.EXAMPLE.COM", 5432, "")
	w.run(0, "apply", auto, noColor)
	id := w.monitor()["id"].(string)
	endpoint := baseURL + "/api/v1/monitors/" + id
	destroyed := false
	t.Cleanup(func() {
		if !destroyed {
			installationRequest(t, http.MethodDelete, endpoint, key, "", http.StatusNoContent)
		}
	})
	assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK), map[string]any{
		"kind": "tcp", "hostname": "db.example.com", "port": float64(5432), "url": nil, "published": false,
		"scheduleMode": "interval", "intervalSeconds": float64(120), "timeoutSeconds": float64(10),
		"failureThreshold": float64(2), "recoveryThreshold": float64(2),
	})
	w.run(0, "plan", "-detailed-exitcode", noColor)
	w.run(0, "state", "rm", address)
	w.run(0, "import", noColor, address, id)
	assertFields(t, w.monitor(), map[string]any{
		"id": id, "kind": "tcp", "hostname": "db.example.com", "port": float64(5432), "url": nil,
	})
	w.run(0, "plan", "-detailed-exitcode", noColor)

	installationRequest(t, http.MethodPatch, baseURL+"/api/operator/monitors/"+id, operatorSecret,
		`{"hostname":"db-operator.example.com","port":5433,"intervalSeconds":300}`, http.StatusOK)
	w.run(0, "apply", "-refresh-only", auto, noColor)
	assertFields(t, w.monitor(), map[string]any{
		"id": id, "hostname": "db-operator.example.com", "port": float64(5433), "interval_seconds": float64(300),
	})
	w.run(2, "plan", "-detailed-exitcode", noColor)

	config("[2001:0DB8:0:0:0:0:0:1]", 8443, `
  schedule_mode = "calendar"
  cron          = "  0  * * * *  "
`)
	w.run(0, "apply", auto, noColor)
	assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK), map[string]any{
		"id": id, "hostname": "2001:db8::1", "port": float64(8443),
		"scheduleMode": "calendar", "cron": "0 * * * *", "timezone": "UTC", "intervalSeconds": float64(300),
	})
	w.run(0, "plan", "-detailed-exitcode", noColor)
	w.run(0, "state", "rm", address)
	w.run(0, "import", noColor, address, id)
	w.run(0, "plan", "-detailed-exitcode", noColor)

	config("2001:db8::1", 8443, `schedule_mode = "interval"`)
	w.run(0, "apply", auto, noColor)
	assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK), map[string]any{
		"id": id, "scheduleMode": "interval", "intervalSeconds": float64(300), "cron": nil, "timezone": nil,
	})
	w.run(0, "plan", "-detailed-exitcode", noColor)

	w.config(`
resource "irisbeat_monitor" "test" {
  name      = "TCP acceptance"
  kind      = "http"
  url       = "https://example.com/"
  published = false
}
`)
	w.run(2, "plan", "-detailed-exitcode", noColor, "-out=replacement.plan")
	var plan struct {
		ResourceChanges []struct {
			Address string
			Change  struct {
				Actions []string
			}
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal([]byte(w.run(0, "show", "-json", "replacement.plan")), &plan); err != nil {
		t.Fatal(err)
	}
	if changes := plan.ResourceChanges; len(changes) != 1 || changes[0].Address != address ||
		strings.Join(changes[0].Change.Actions, ",") != "delete,create" {
		t.Fatalf("expected replacement plan for %s, got %+v", address, changes)
	}

	config("2001:db8::1", 8443, `schedule_mode = "interval"`)
	w.run(0, "destroy", auto, noColor)
	destroyed = true
	installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusNotFound)
}

func TestMonitorTargetValidation(t *testing.T) {
	w := workspace(t, "http://127.0.0.1:1", "unused")
	for _, test := range []struct {
		name, kind, target, diagnostic string
	}{
		{"TCP requires hostname", "tcp", "port = 5432", "TCP Monitors require hostname"},
		{"TCP requires port", "tcp", `hostname = "db.example.com"`, "TCP Monitors require port"},
		{"TCP rejects URL", "tcp", `hostname = "db.example.com"
port = 5432
url = "https://example.com/"`, "url is only available for HTTP Monitors"},
		{"TCP checks port bounds", "tcp", `hostname = "db.example.com"
port = 65536`, "Use an integer from 1 to 65535"},
		{"HTTP requires URL", "http", "", "HTTP Monitors require url"},
		{"HTTP rejects TCP fields", "http", `url = "https://example.com/"
hostname = "db.example.com"
port = 5432`, "only available for TCP Monitors"},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := w
			w.t = t
			w.config(fmt.Sprintf(`
resource "irisbeat_monitor" "test" {
  name      = "Target validation"
  kind      = %q
  published = false
  %s
}
`, test.kind, test.target))
			out := w.run(1, "plan", "-no-color")
			if !strings.Contains(out, test.diagnostic) {
				t.Fatalf("expected diagnostic %q\n%s", test.diagnostic, out)
			}
		})
	}
}
