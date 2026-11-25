// SPDX-License-Identifier: AGPL-3.0-only
package acceptance_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const address = "irisbeat_monitor.test"

type cliWorkspace struct {
	t        *testing.T
	dir, cli string
}

func workspace(t *testing.T, baseURL, key string) cliWorkspace {
	t.Helper()
	cli := os.Getenv("IRISBEAT_TEST_CLI")
	binary := os.Getenv("IRISBEAT_TEST_PROVIDER")
	if cli == "" || binary == "" {
		t.Skip("set IRISBEAT_TEST_CLI and IRISBEAT_TEST_PROVIDER to run CLI acceptance")
	}
	t.Setenv("IRISBEAT_BASE_URL", baseURL)
	t.Setenv("IRISBEAT_API_KEY", key)
	t.Setenv("TF_IN_AUTOMATION", "1")
	t.Setenv("TF_LOG", "")
	t.Setenv("TF_LOG_CORE", "")
	t.Setenv("TF_LOG_PROVIDER", "")
	t.Setenv("TF_LOG_PATH", "")
	dir := t.TempDir()
	rc := filepath.Join(dir, "cli.tfrc")
	config := fmt.Sprintf("provider_installation {\n  dev_overrides {\n    \"registry.terraform.io/webriot/irisbeat\" = %q\n  }\n  direct {}\n}\n", filepath.Dir(binary))
	if err := os.WriteFile(rc, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TF_CLI_CONFIG_FILE", rc)
	return cliWorkspace{t, dir, cli}
}

func (w cliWorkspace) config(resource string) {
	w.t.Helper()
	const required = `
terraform {
  required_providers {
    irisbeat = {
      source = "registry.terraform.io/webriot/irisbeat"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(w.dir, "main.tf"), []byte(required+resource), 0600); err != nil {
		w.t.Fatal(err)
	}
}

func (w cliWorkspace) run(wantExit int, args ...string) string {
	w.t.Helper()
	cmd := exec.Command(w.cli, args...)
	cmd.Dir = w.dir
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		w.t.Fatal(err)
	}
	if code != wantExit {
		w.t.Fatalf("%s exited %d, want %d\n%s", strings.Join(args, " "), code, wantExit, out)
	}
	return string(out)
}

func (w cliWorkspace) monitor() map[string]any {
	w.t.Helper()
	return w.resourceState(address)
}

func (w cliWorkspace) resourceState(address string) map[string]any {
	w.t.Helper()
	var state struct {
		Values struct {
			Root struct {
				Resources []struct {
					Address string
					Values  map[string]any
				}
			} `json:"root_module"`
		}
	}
	if err := json.Unmarshal([]byte(w.run(0, "show", "-json")), &state); err != nil {
		w.t.Fatal(err)
	}
	for _, resource := range state.Values.Root.Resources {
		if resource.Address == address {
			return resource.Values
		}
	}
	w.t.Fatalf("%s is missing from state", address)
	return nil
}

func installationRequest(t *testing.T, method, endpoint, key, body string, wantStatus int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(method, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s returned HTTP %d, want %d", method, req.URL.Path, resp.StatusCode, wantStatus)
	}
	if wantStatus == http.StatusNoContent {
		return nil
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func cleanupResource(t *testing.T, endpoint, key string) {
	t.Helper()
	t.Cleanup(func() {
		// The API cleanup also covers failures between state removal and re-import.
		req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
		if err != nil {
			t.Error(err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+key)
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
			t.Errorf("cleanup status = %d", resp.StatusCode)
		}
	})
}

func assertFields(t *testing.T, actual, expected map[string]any) {
	t.Helper()
	for key, want := range expected {
		if actual[key] != want {
			t.Errorf("%s = %v, want %v", key, actual[key], want)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func TestHTTPMonitorLifecycle(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	operatorSecret := os.Getenv("IRISBEAT_TEST_INSTALLATION_SECRET")
	if baseURL == "" || key == "" || operatorSecret == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL, IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_INSTALLATION_SECRET for disposable Irisbeat with a 120-second check floor")
	}
	w := workspace(t, baseURL, key)
	const apply = "-auto-approve"
	const noColor = "-no-color"
	config := func(url string, published bool, schedule string) {
		w.config(fmt.Sprintf(`
resource "irisbeat_monitor" "test" {
  name      = "  HTTP acceptance  "
  kind      = "http"
  url       = %q
  published = %t
  %s
}
`, url, published, schedule))
	}
	config("https://EXAMPLE.com:443#fragment", false, "")
	w.run(0, "apply", apply, noColor)
	id := w.monitor()["id"].(string)
	endpoint := baseURL + "/api/v1/monitors/" + id
	cleanupResource(t, endpoint, key)
	assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK), map[string]any{
		"name": "HTTP acceptance", "url": "https://example.com/", "published": false,
		"scheduleMode": "interval", "intervalSeconds": float64(120), "timeoutSeconds": float64(10),
		"failureThreshold": float64(2), "recoveryThreshold": float64(2),
	})
	w.run(0, "plan", "-detailed-exitcode", noColor)

	w.run(0, "state", "rm", address)
	w.run(0, "import", noColor, address, id)
	assertFields(t, w.monitor(), map[string]any{"id": id, "name": "HTTP acceptance", "url": "https://example.com/"})

	w.run(0, "plan", "-detailed-exitcode", noColor)

	installationRequest(t, http.MethodPatch, baseURL+"/api/operator/monitors/"+id, operatorSecret,
		`{"url":"https://example.com/operator","published":true,"intervalSeconds":300}`, http.StatusOK)
	w.run(0, "apply", "-refresh-only", apply, noColor)
	assertFields(t, w.monitor(), map[string]any{"id": id, "url": "https://example.com/operator", "published": true, "interval_seconds": float64(300)})
	w.run(2, "plan", "-detailed-exitcode", noColor)

	config("https://example.com/updated", false, `
  schedule_mode = "calendar"
  cron          = "  0  * * * *  "
`)
	w.run(0, "apply", apply, noColor)
	assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK), map[string]any{
		"id": id, "url": "https://example.com/updated", "published": false, "intervalSeconds": float64(300),
		"scheduleMode": "calendar", "cron": "0 * * * *", "timezone": "UTC",
	})
	w.run(0, "plan", "-detailed-exitcode", noColor)
	w.run(0, "state", "rm", address)
	w.run(0, "import", noColor, address, id)
	w.run(0, "plan", "-detailed-exitcode", noColor)
	config("https://example.com/updated", true, `
  schedule_mode    = "interval"
  interval_seconds = 60
`)
	out := w.run(1, "apply", apply, noColor)
	if !strings.Contains(out, "installation check floor") {
		t.Fatal("expected the floor rejection to be actionable")
	}
	assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK), map[string]any{
		"scheduleMode": "calendar", "published": false, "intervalSeconds": float64(300),
	})
	config("https://example.com/updated", true, `schedule_mode = "interval"`)
	w.run(0, "apply", apply, noColor)
	assertFields(t, w.monitor(), map[string]any{
		"id": id, "schedule_mode": "interval", "interval_seconds": float64(300), "cron": nil, "timezone": nil, "published": true,
	})
	w.run(0, "plan", "-detailed-exitcode", noColor)
	config("https://example.com/updated", true, `
  interval_seconds   = 360
  timeout_seconds    = 14
  failure_threshold  = 4
  recovery_threshold = 3
`)
	w.run(0, "apply", apply, noColor)
	assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK), map[string]any{
		"id": id, "intervalSeconds": float64(360), "timeoutSeconds": float64(14),
		"failureThreshold": float64(4), "recoveryThreshold": float64(3),
	})
	w.run(0, "plan", "-detailed-exitcode", noColor)
	w.run(0, "destroy", apply, noColor)
	installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusNotFound)
}

func TestAPIResponseHandling(t *testing.T) {
	const id = "monitor-from-alias"
	const aliasKey = "alias-credential"
	const noColor = "-no-color"
	const notFound = `{"error":"Monitor not found"}`
	var status atomic.Int32
	var generic404 atomic.Bool
	status.Store(http.StatusOK)
	alias := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/monitors/"+id || r.Header.Get("Authorization") != "Bearer "+aliasKey {
			t.Error("API request did not use the alias installation and key")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		code := int(status.Load())
		w.WriteHeader(code)
		switch {
		case code == http.StatusOK:
			_, _ = io.WriteString(w, `{"id":"monitor-from-alias","name":"HTTP acceptance","kind":"http","url":"https://example.com/","published":true,"scheduleMode":"interval","intervalSeconds":60,"timeoutSeconds":10,"failureThreshold":2,"recoveryThreshold":2,"destinationIds":[]}`)
		case code == http.StatusNotFound && !generic404.Load():
			_, _ = io.WriteString(w, notFound)
		default:
			_, _ = io.WriteString(w, `{"error":"raw-response-secret"}`)
		}
	}))
	defer alias.Close()
	defaultInstallation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("received aliased request at the default installation")
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer defaultInstallation.Close()

	w := workspace(t, defaultInstallation.URL, "default-credential")
	w.config(fmt.Sprintf(`
provider "irisbeat" {}
provider "irisbeat" {
  alias    = "other"
  base_url = %q
  api_key  = %q
}
resource "irisbeat_monitor" "test" {
  provider  = irisbeat.other
  name      = " HTTP acceptance "
  kind      = "http"
  url       = "https://EXAMPLE.com:443#fragment"
  published = true
}
`, alias.URL, aliasKey))
	w.run(0, "import", noColor, address, id)
	w.run(0, "plan", "-detailed-exitcode", noColor)

	for _, failure := range []struct {
		status   int
		expected string
	}{
		{http.StatusUnauthorized, "invalid, expired, or revoked"},
		{http.StatusForbidden, "insufficient permission"},
		{http.StatusNotFound, "HTTP 404"},
		{http.StatusServiceUnavailable, "HTTP 503"},
	} {
		generic404.Store(true)
		status.Store(int32(failure.status))
		t.Setenv("TF_LOG_PROVIDER", "TRACE")
		out := w.run(1, "apply", "-refresh-only", "-auto-approve", noColor)
		t.Setenv("TF_LOG_PROVIDER", "")
		if !strings.Contains(out, failure.expected) || strings.Contains(out, aliasKey) || strings.Contains(out, "raw-response-secret") {
			t.Fatalf("expected safe refresh diagnostic containing %q", failure.expected)
		}
		assertFields(t, w.monitor(), map[string]any{"id": id})
	}

	status.Store(http.StatusNotFound)
	generic404.Store(true)
	w.run(1, "destroy", "-refresh=false", "-auto-approve", noColor)
	assertFields(t, w.monitor(), map[string]any{"id": id})
	generic404.Store(false)
	w.run(0, "destroy", "-refresh=false", "-auto-approve", noColor)
	if out := w.run(0, "state", "list"); strings.Contains(out, address) {
		t.Fatal("destroy should remove state for a deleted Monitor")
	}
	out := w.run(1, "import", noColor, address, id)
	if !strings.Contains(out, "does not exist in this Irisbeat installation") {
		t.Fatal("expected missing import diagnostic")
	}

	status.Store(http.StatusOK)
	w.run(0, "import", noColor, address, id)
	status.Store(http.StatusNotFound)
	w.run(0, "apply", "-refresh-only", "-auto-approve", noColor)
	if out := w.run(0, "state", "list"); strings.Contains(out, address) {
		t.Fatal("refresh should remove state for a deleted Monitor")
	}
}
