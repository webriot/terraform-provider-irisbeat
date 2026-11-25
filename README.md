# Irisbeat provider

Manage HTTP, TCP, and Heartbeat Monitors and webhook Notification destinations on a self-hosted Irisbeat installation with Terraform or OpenTofu. The provider uses the [Irisbeat v1 automation API](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/api-v1.md).

## Run a local build

Build with Go 1.25 or later. The same binary serves plugin protocol 6 to both CLIs.

```sh
provider_dir="$(mktemp -d)"
go build -o "$provider_dir/terraform-provider-irisbeat" .
export TF_CLI_CONFIG_FILE="$provider_dir/provider.tfrc"
cat > "$TF_CLI_CONFIG_FILE" <<EOF_CONFIG
provider_installation {
  dev_overrides {
    "registry.terraform.io/webriot/irisbeat" = "$provider_dir"
  }
  direct {}
}
EOF_CONFIG
```

Issue an [installation-scoped Write API key](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/api-v1.md#key-administration) in Irisbeat. Set `IRISBEAT_BASE_URL` to the installation URL and inject the key as `IRISBEAT_API_KEY`. An explicit `base_url` or sensitive `api_key` takes precedence over its environment variable. Use provider aliases with their own URL and key for additional installations.

In a separate directory, save this configuration as `main.tf` and run `terraform plan` or `tofu plan` using the override. The override loads the local binary directly, so you can run the workflow without `init`.

```hcl
terraform {
  required_providers {
    irisbeat = {
      source = "registry.terraform.io/webriot/irisbeat"
    }
  }
}

resource "irisbeat_monitor" "website" {
  name      = "Website"
  kind      = "http"
  url       = "https://example.com/"
  published = false
}

resource "irisbeat_monitor" "database" {
  name      = "Database"
  kind      = "tcp"
  hostname  = "db.example.com"
  port      = 5432
  published = false
}
```

Use an HTTP(S) URL without embedded credentials for HTTP and a DNS name or IP address with a port from 1 to 65535 for TCP. The provider rejects known malformed HTTP targets during planning. Target and schedule edits preserve the Monitor ID.

### Changing Monitor kind

A kind change plans a native replacement with a new ID. If any configured value is unknown during planning, apply its upstream dependencies separately before planning the kind change.

By default, Terraform and OpenTofu delete the existing Monitor before creating its replacement. Irisbeat checks installation-specific constraints during the create. The v0.5.0 automation API has no Write-key endpoint to read the check floor or validate a proposed Monitor without saving it. The separate [Operator API](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/monitor-api.md#outbound-check-floor) requires the installation secret. It exposes the floor in Operator snapshots and can preview Calendar schedules.

To keep the old Monitor until the replacement is created, set `lifecycle { create_before_destroy = true }` on the resource. On a disposable Irisbeat v0.5.0 installation with a 120-second check floor, Terraform 1.14.0 and OpenTofu 1.12.5 both rejected a malformed HTTP URL at plan time in either lifecycle order. A valid URL with a 60-second interval passed planning and Irisbeat rejected the create during apply. The default order removed the old Heartbeat. With `create_before_destroy`, the failed create preserved its ID, webhook assignment, and last receipt. A successful replacement starts with a new ID and history. Omitted routing starts empty.

Irisbeat supplies omitted timing on create and enforces the installation's check floor. Set `schedule_mode = "calendar"` with `cron` to use a Calendar schedule. Set `schedule_mode = "interval"` and remove `cron` and `timezone` to switch back. Irisbeat retains the saved `interval_seconds` for HTTP and TCP Monitors in Calendar mode. Supply a new interval when switching back if the saved interval falls below the check floor.

To adopt a Monitor, declare its matching saved configuration and run `terraform import irisbeat_monitor.website <monitor-id>` or `tofu import irisbeat_monitor.website <monitor-id>` using its ID from the same installation. If an interrupted create leaves the outcome unknown, check Irisbeat and import any existing Monitor before attempting another create.

## Heartbeat Monitors

```hcl
resource "irisbeat_monitor" "nightly_job" {
  name          = "Nightly job"
  kind          = "heartbeat"
  published     = false
  schedule_mode = "calendar"
  cron          = "0 2 * * *"
  timezone      = "UTC"
  grace_seconds = 300
}

output "nightly_job_receipt_path" {
  value = irisbeat_monitor.nightly_job.heartbeat_path
}
```

For Interval mode, omit the Calendar fields and set `schedule_mode = "interval"`, or omit the mode when creating a Monitor. Declaring `interval_seconds`, `silence_seconds`, or `recovery_spacing_seconds` implies Interval mode when `schedule_mode` is omitted. Both modes support `recovery_threshold`. Irisbeat supplies omitted timing defaults on create. Schedule changes preserve the Monitor ID. Changing kind replaces the Monitor.

Configure the job to send an HTTPS POST to the installation URL plus `heartbeat_path`. Set the installation secret in the job's secret store and send it as the Bearer credential. See the [Heartbeat receipt API](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/heartbeat-api.md#send-a-receipt).

## Manage a webhook Notification destination

```hcl
variable "alert_webhook_url" {
  type      = string
  sensitive = true
}

resource "irisbeat_notification_destination" "alerts" {
  channel     = "webhook"
  webhook_url = var.alert_webhook_url
}
```

Irisbeat generates a name when you omit `name`. Set `name` to rename the destination in place. Changing the saved webhook URL replaces the destination. Deleting the old destination clears all its Monitor assignments. Irisbeat normalizes recognized Discord webhook URLs to use the `/slack` suffix.

When the webhook URL is unknown during planning, the plan keeps the existing destination. If the URL resolves to the same saved address, apply preserves the ID and Monitor assignments. If the address changes, the CLI stops at the destination with an inconsistent final plan error. Upstream changes may already have applied. Run a fresh plan to review the replacement, or apply the upstream dependency separately before planning.

Run `terraform import irisbeat_notification_destination.alerts <destination-id>` or `tofu import irisbeat_notification_destination.alerts <destination-id>` to adopt an existing webhook. The URL is sensitive in routine CLI output and is stored in plan and state files. If a create ends with an unknown outcome, check Irisbeat and import any existing destination before attempting another create.

## Verify

`go test ./...` runs the deterministic HTTP tests. To run CLI acceptance, use a disposable Irisbeat v0.5.0 installation with `MIN_CHECK_INTERVAL_SECONDS=120`. Inject its Write API key as `IRISBEAT_TEST_API_KEY` and the installation secret for independent Operator edits as `IRISBEAT_TEST_INSTALLATION_SECRET`. The tests create and delete Monitors and webhook Notification destinations.

```sh
export IRISBEAT_TEST_BASE_URL=http://127.0.0.1:8787
export IRISBEAT_TEST_PROVIDER="$provider_dir/terraform-provider-irisbeat"
IRISBEAT_TEST_CLI="$(command -v terraform)" go test ./acceptance -count=1 -v
IRISBEAT_TEST_CLI="$(command -v tofu)" go test ./acceptance -count=1 -v
```

Tested with Irisbeat v0.5.0, Terraform v1.14.0, and OpenTofu v1.12.5.

## License

Copyright 2026 Webriot. This project's own code is open source under [AGPL-3.0-only](LICENSE). Third-party dependencies retain their own licenses. See [NOTICE](NOTICE).
