// SPDX-License-Identifier: AGPL-3.0-only
package acceptance_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestHeartbeatMonitorLifecycle(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	secret := os.Getenv("IRISBEAT_TEST_INSTALLATION_SECRET")
	if baseURL == "" || key == "" || secret == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL, IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_INSTALLATION_SECRET for disposable Irisbeat")
	}
	const noColor = "-no-color"
	const autoApprove = "-auto-approve"
	const calendar = `schedule_mode = "calendar"
		cron = "0   */6 * * *"`
	const interval = `schedule_mode = "interval"
		interval_seconds = 90
		silence_seconds = 240
		recovery_spacing_seconds = 75
		recovery_threshold = 3`

	for _, mode := range []string{"interval", "calendar"} {
		t.Run(mode, func(t *testing.T) {
			w := workspace(t, baseURL, key)
			config := func(kind, fields string) {
				w.config(fmt.Sprintf(`
resource "irisbeat_monitor" "test" {
  name      = "Heartbeat acceptance"
  kind      = %q
  published = false
  %s
}
output "receipt" {
  value = irisbeat_monitor.test.heartbeat_path
}
`, kind, fields))
			}
			fields := ""
			if mode == "calendar" {
				fields = calendar
			}
			config("heartbeat", fields)
			w.run(0, "apply", autoApprove, noColor)
			id := w.monitor()["id"].(string)
			receipt := "/heartbeat/" + id
			endpoint := baseURL + "/api/v1/monitors/" + id
			assertFields(t, w.monitor(), map[string]any{
				"heartbeat_path": receipt, "schedule_mode": mode, "recovery_threshold": float64(2),
				"url": nil, "timeout_seconds": nil, "failure_threshold": nil,
			})
			if mode == "interval" {
				assertFields(t, w.monitor(), map[string]any{
					"interval_seconds": float64(60), "silence_seconds": float64(180),
					"recovery_spacing_seconds": float64(60), "grace_seconds": nil, "cron": nil, "timezone": nil,
				})
			} else {
				assertFields(t, w.monitor(), map[string]any{
					"timezone": "UTC", "grace_seconds": float64(30),
					"interval_seconds": nil, "silence_seconds": nil, "recovery_spacing_seconds": nil,
				})
			}
			installationRequest(t, http.MethodPost, baseURL+receipt, key, "", http.StatusUnauthorized)
			installationRequest(t, http.MethodPost, baseURL+receipt, secret, "", http.StatusNoContent)
			w.run(0, "plan", "-detailed-exitcode", noColor)
			w.run(0, "state", "rm", address)
			w.run(0, "import", noColor, address, id)
			assertFields(t, w.monitor(), map[string]any{"id": id, "heartbeat_path": receipt, "schedule_mode": mode})
			w.run(0, "plan", "-detailed-exitcode", noColor)

			config("heartbeat", fields+"\nrecovery_threshold = 2")
			installationRequest(t, http.MethodPatch, baseURL+"/api/operator/monitors/"+id, secret,
				`{"recoveryThreshold":4}`, http.StatusOK)
			w.run(0, "apply", "-refresh-only", autoApprove, noColor)
			assertFields(t, w.monitor(), map[string]any{"id": id, "recovery_threshold": float64(4)})
			w.heartbeatPlan([]string{"update"}, receipt)
			w.run(0, "apply", autoApprove, noColor)
			assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK),
				map[string]any{"id": id, "recoveryThreshold": float64(2)})

			nextMode, nextFields := "interval", interval
			expected := map[string]any{"intervalSeconds": float64(90), "silenceSeconds": float64(240),
				"recoverySpacingSeconds": float64(75), "recoveryThreshold": float64(3), "graceSeconds": nil}
			if mode == "interval" {
				nextMode, nextFields = "calendar", calendar+"\ntimezone = \"America/New_York\"\ngrace_seconds = 120"
				expected = map[string]any{"cron": "0 */6 * * *", "timezone": "America/New_York",
					"graceSeconds": float64(120), "intervalSeconds": nil, "silenceSeconds": nil, "recoverySpacingSeconds": nil}
			}
			config("heartbeat", nextFields)
			w.heartbeatPlan([]string{"update"}, receipt)
			w.run(0, "apply", autoApprove, noColor)
			expected["id"], expected["scheduleMode"] = id, nextMode
			assertFields(t, installationRequest(t, http.MethodGet, endpoint, key, "", http.StatusOK), expected)
			w.run(0, "plan", "-detailed-exitcode", noColor)

			currentID := id
			for _, target := range []struct{ kind, fields string }{
				{"http", `url = "https://example.com/"`},
				{"tcp", `hostname = "db.example.com"
port = 5432`},
			} {
				config(target.kind, target.fields)
				replace := []string{"delete", "create"}
				w.heartbeatPlan(replace, "")
				w.run(0, "apply", noColor, "heartbeat.tfplan")
				outboundID := w.monitor()["id"].(string)
				if outboundID == currentID {
					t.Fatal("replacement reused the Heartbeat Monitor ID")
				}
				assertFields(t, w.monitor(), map[string]any{
					"kind": target.kind, "heartbeat_path": nil, "silence_seconds": nil, "grace_seconds": nil,
				})
				installationRequest(t, http.MethodGet, baseURL+"/api/v1/monitors/"+currentID, key, "", http.StatusNotFound)
				w.run(0, "plan", "-detailed-exitcode", noColor)

				config("heartbeat", fields)
				w.heartbeatPlan(replace, "")
				w.run(0, "apply", noColor, "heartbeat.tfplan")
				currentID = w.monitor()["id"].(string)
				if currentID == outboundID || currentID == id {
					t.Fatal("replacement reused a Monitor ID")
				}
				assertFields(t, w.monitor(), map[string]any{
					"heartbeat_path": "/heartbeat/" + currentID, "hostname": nil, "port": nil, "url": nil,
				})
				installationRequest(t, http.MethodGet, baseURL+"/api/v1/monitors/"+outboundID, key, "", http.StatusNotFound)
				installationRequest(t, http.MethodPost, baseURL+"/heartbeat/"+currentID, secret, "", http.StatusNoContent)
				w.run(0, "plan", "-detailed-exitcode", noColor)
			}
			w.run(0, "destroy", autoApprove, noColor)
			installationRequest(t, http.MethodGet, baseURL+"/api/v1/monitors/"+currentID, key, "", http.StatusNotFound)
		})
	}
}

func (w cliWorkspace) heartbeatPlan(actions []string, receipt string) map[string]any {
	w.t.Helper()
	w.run(2, "plan", "-detailed-exitcode", "-no-color", "-out=heartbeat.tfplan")
	var plan struct {
		ResourceChanges []struct {
			Address string
			Change  struct {
				Actions []string
				After   map[string]any
			}
		} `json:"resource_changes"`
		OutputChanges map[string]struct{ After string } `json:"output_changes"`
	}
	if err := json.Unmarshal([]byte(w.run(0, "show", "-json", "heartbeat.tfplan")), &plan); err != nil {
		w.t.Fatal(err)
	}
	for _, change := range plan.ResourceChanges {
		if change.Address != address {
			continue
		}
		if !slices.Equal(change.Change.Actions, actions) {
			w.t.Fatalf("planned actions %s, want %s", change.Change.Actions, actions)
		}
		if receipt != "" && plan.OutputChanges["receipt"].After != receipt {
			w.t.Fatal("existing receipt path must be known and unchanged in the plan")
		}
		return change.Change.After
	}
	w.t.Fatalf("plan has no change for %s (wanted %s)", address, strings.Join(actions, ", "))
	return nil
}

func TestMonitorInputsResolvedAtApply(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	if baseURL == "" || key == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL and IRISBEAT_TEST_API_KEY for disposable Irisbeat")
	}
	for _, tc := range []struct {
		name, kind, schedule, input, fields, diagnostic string
	}{
		{
			name: "null HTTP fields and Calendar timing on Interval", kind: "heartbeat",
			input: `{ url = tostring(null), number = tonumber(null), timezone = tostring(null) }`,
			fields: `url = terraform_data.inputs.output.url
  timeout_seconds = terraform_data.inputs.output.number
  failure_threshold = terraform_data.inputs.output.number
  grace_seconds = terraform_data.inputs.output.number
  timezone = terraform_data.inputs.output.timezone`,
		},
		{
			name: "null Interval timing on Calendar", kind: "heartbeat",
			schedule: `schedule_mode = "calendar"
  cron = "0 * * * *"`,
			input: `tonumber(null)`,
			fields: `interval_seconds = terraform_data.inputs.output
  silence_seconds = terraform_data.inputs.output
  recovery_spacing_seconds = terraform_data.inputs.output`,
		},
		{
			name: "null Heartbeat timing on HTTP", kind: "http",
			input: `tonumber(null)`,
			fields: `url = "https://example.com/"
  silence_seconds = terraform_data.inputs.output`,
		},
		{
			name: "null TCP target on Heartbeat", kind: "heartbeat",
			input: `{ hostname = tostring(null), port = tonumber(null) }`,
			fields: `hostname = terraform_data.inputs.output.hostname
  port = terraform_data.inputs.output.port`,
		},
		{
			name: "disallowed TCP target on Heartbeat", kind: "heartbeat",
			input: `{ hostname = "db.example.com", port = 5432 }`,
			fields: `hostname = terraform_data.inputs.output.hostname
  port = terraform_data.inputs.output.port`, diagnostic: "TCP kind required",
		},
		{
			name: "disallowed Heartbeat timing on TCP", kind: "tcp", input: "250",
			fields: `hostname = "db.example.com"
  port = 5432
  silence_seconds = terraform_data.inputs.output`, diagnostic: "Heartbeat kind required",
		},
		{
			name: "invalid TCP port", kind: "tcp", input: "65536",
			fields: `hostname = "db.example.com"
  port = terraform_data.inputs.output`, diagnostic: "Invalid TCP port",
		},
		{
			name: "disallowed HTTP field", kind: "heartbeat",
			input: `{ url = "https://example.com/" }`, fields: "url = terraform_data.inputs.output.url",
			diagnostic: "HTTP kind required",
		},
		{
			name: "disallowed Heartbeat field", kind: "http", input: "250",
			fields: `url = "https://example.com/"
  silence_seconds = terraform_data.inputs.output`, diagnostic: "Heartbeat kind required",
		},
		{
			name: "disallowed Interval timing", kind: "heartbeat",
			schedule: `schedule_mode = "calendar"
  cron = "0 * * * *"`,
			input: "250", fields: "silence_seconds = terraform_data.inputs.output", diagnostic: "Interval mode required",
		},
		{
			name: "disallowed Calendar timing", kind: "heartbeat",
			input: "45", fields: "grace_seconds = terraform_data.inputs.output", diagnostic: "Calendar mode required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := workspace(t, baseURL, key)
			config := fmt.Sprintf(`
resource "terraform_data" "inputs" {
  input = %s
}
resource "irisbeat_monitor" "test" {
  name = "Resolved input acceptance"
  kind = %q
  published = false
  %s
  %s
}
`, tc.input, tc.kind, tc.schedule, tc.fields)
			w.config(config)
			w.run(2, "plan", "-detailed-exitcode", "-no-color", "-out=inputs.tfplan")
			if tc.diagnostic != "" {
				out := w.run(1, "apply", "-no-color", "inputs.tfplan")
				if !strings.Contains(out, tc.diagnostic) {
					t.Fatalf("expected %q after resolving the input\n%s", tc.diagnostic, out)
				}
				return
			}

			w.run(0, "apply", "-no-color", "inputs.tfplan")
			assertFields(t, w.monitor(), map[string]any{"kind": tc.kind})
			w.run(0, "plan", "-detailed-exitcode", "-no-color")
			id := w.monitor()["id"]
			w.config(strings.Replace(config, "Resolved input acceptance", "Updated Monitor", 1))
			w.run(2, "plan", "-detailed-exitcode", "-no-color", "-replace=terraform_data.inputs", "-out=inputs.tfplan")
			w.run(0, "apply", "-no-color", "inputs.tfplan")
			assertFields(t, w.monitor(), map[string]any{"id": id, "name": "Updated Monitor"})
			w.run(0, "plan", "-detailed-exitcode", "-no-color")
			w.run(0, "destroy", "-auto-approve", "-no-color")
		})
	}
}

func TestHeartbeatImplicitIntervalDrift(t *testing.T) {
	baseURL, key := os.Getenv("IRISBEAT_TEST_BASE_URL"), os.Getenv("IRISBEAT_TEST_API_KEY")
	secret := os.Getenv("IRISBEAT_TEST_INSTALLATION_SECRET")
	if baseURL == "" || key == "" || secret == "" {
		t.Skip("set IRISBEAT_TEST_BASE_URL, IRISBEAT_TEST_API_KEY and IRISBEAT_TEST_INSTALLATION_SECRET for disposable Irisbeat")
	}
	w := workspace(t, baseURL, key)
	w.config(`
resource "irisbeat_monitor" "test" {
  name = "Implicit Interval acceptance"
  kind = "heartbeat"
  published = false
  silence_seconds = 250
}
output "receipt" {
  value = irisbeat_monitor.test.heartbeat_path
}
`)
	w.run(0, "apply", "-auto-approve", "-no-color")
	id := w.monitor()["id"].(string)
	receipt := "/heartbeat/" + id
	t.Cleanup(func() {
		installationRequest(t, http.MethodDelete, baseURL+"/api/v1/monitors/"+id, key, "", http.StatusNoContent)
	})

	installationRequest(t, http.MethodPatch, baseURL+"/api/operator/monitors/"+id, secret,
		`{"scheduleMode":"calendar","cron":"0 * * * *"}`, http.StatusOK)
	w.run(0, "apply", "-refresh-only", "-auto-approve", "-no-color")
	assertFields(t, w.monitor(), map[string]any{"id": id, "schedule_mode": "calendar", "heartbeat_path": receipt})

	after := w.heartbeatPlan([]string{"update"}, receipt)
	assertFields(t, after, map[string]any{"id": id, "schedule_mode": "interval", "heartbeat_path": receipt})
	w.run(0, "apply", "-no-color", "heartbeat.tfplan")
	assertFields(t, w.monitor(), map[string]any{"id": id, "schedule_mode": "interval", "heartbeat_path": receipt})
	assertFields(t, installationRequest(t, http.MethodGet, baseURL+"/api/v1/monitors/"+id, key, "", http.StatusOK),
		map[string]any{"id": id, "scheduleMode": "interval", "silenceSeconds": float64(250), "cron": nil, "timezone": nil})
	w.run(0, "plan", "-detailed-exitcode", "-no-color")
}
