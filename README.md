# Irisbeat provider

Manage HTTP, TCP, and Heartbeat Monitors and Notification destinations for webhook and email on a self-hosted Irisbeat installation with Terraform or OpenTofu. The provider uses the [Irisbeat v1 automation API](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/api-v1.md).

## Compatibility

| Component | Tested minimum |
| --- | --- |
| Irisbeat (self-hosted) | v0.5.0 |
| Terraform | v1.14.0 |
| OpenTofu | v1.12.5 |

## Install from a registry

After the provider is published, declare it in your root module and run `terraform init` or `tofu init`. The short source address resolves through each CLI's own public registry.

```hcl
terraform {
  required_providers {
    irisbeat = {
      source  = "webriot/irisbeat"
      version = "~> 0.1.0"
    }
  }
}
```

Issue an installation-scoped Write API key. Send the installation secret as the Bearer credential to [`POST /api/operator/api-keys`](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/api-v1.md#issue-a-key) with the JSON body `{"name":"Infrastructure","role":"write"}`. Save the returned `secret` in your secret store. Set `IRISBEAT_BASE_URL` to the installation origin, and inject that Write key as `IRISBEAT_API_KEY`. An explicit `base_url` or sensitive `api_key` takes precedence over its environment variable. Use provider aliases with their own URL and key for additional installations.

Save plan and state files in protected storage with limited access. Both CLIs can retain sensitive addresses in those files and expose values in machine-readable output. See [Terraform sensitive data](https://developer.hashicorp.com/terraform/language/manage-sensitive-data) and [OpenTofu sensitive data](https://opentofu.org/docs/language/state/sensitive-data/).

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

In a separate directory, save this configuration as `main.tf` and run `terraform plan` or `tofu plan` using the override and the same Write API key setup. The override loads the local binary directly and skips `init`.

## Manage Monitors

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

A kind change plans a native replacement with a new ID. If any configured value or member of `destination_ids` is unknown during planning, apply its upstream dependencies separately before planning the kind change.

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

Configure the job to send an HTTPS POST to the installation URL plus `heartbeat_path`. Set the installation secret in the job's secret store and send it as the Bearer credential. For example, inject `IRISBEAT_HEARTBEAT_PATH` from the output above and set `IRISBEAT_INSTALLATION_SECRET` in the job's secret store.

```sh
curl --fail --silent --show-error --request POST \
  --header @- "${IRISBEAT_BASE_URL}${IRISBEAT_HEARTBEAT_PATH}" <<EOF_HEADER
Authorization: Bearer ${IRISBEAT_INSTALLATION_SECRET}
EOF_HEADER
```

See the [Heartbeat receipt API](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/heartbeat-api.md#send-a-receipt).

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

## Manage an email Notification destination

[Enable email delivery](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/install.md#optional-email-confirmation) on the installation, then configure an inbox.

```hcl
variable "alerts_inbox" {
  type      = string
  sensitive = true
}

resource "irisbeat_notification_destination" "alerts_email" {
  channel       = "email"
  name          = "Operations email"
  email_address = var.alerts_inbox
}
```

The computed `confirmed` field starts at `false` for a new inbox. After the recipient opens the confirmation link and submits the form, refresh with `terraform apply -refresh-only` or `tofu apply -refresh-only`. The field is `null` for webhooks.

Use the same ID import workflow as for a webhook. Import reads the saved address and confirmation state. Both CLIs hide the address in routine output and store it in plan and state files. Irisbeat trims the address, lowercases the domain, and preserves local-part case. An equivalent address produces an empty plan. Renaming preserves the destination ID. Changing the channel or saved address replaces the destination. If the email address is unknown during planning, the same apply guidance as for a computed webhook URL applies.

### Switch inboxes without interrupting routing

1. Add a second email destination at a separate resource address with a distinct name and inbox. Apply with the original destination still configured and assigned to the Monitor.
2. Have the new recipient confirm the inbox. Refresh state and check that the new destination has `confirmed = true`.
3. Replace the original ID in the Monitor's `destination_ids` with the new ID. Keep any other assignments in the set and apply the routing change with both destinations still configured.
4. Remove the original destination from configuration and apply. Deleting it clears any remaining assignments, including those on Monitors outside this configuration.

If you try assigning a pending inbox, Irisbeat rejects the whole Monitor write and preserves its saved configuration and routing. The new destination remains managed even if both changes were in that apply. Have the recipient confirm the inbox, then reapply the Monitor change.

## Manage Monitor routing

Add `destination_ids = [irisbeat_notification_destination.alerts.id]` to a Monitor to manage its complete set of Notification destination IDs. Reordering IDs produces an empty plan. Apply repairs changes made in the Operator when an explicit set is configured.

Omit `destination_ids` to observe routing in state and leave it under Operator control. This also preserves the saved assignments when importing a Monitor, editing other fields, or removing a previously configured set. Set `destination_ids = []` to clear all assignments.

Irisbeat rejects the entire Monitor write if any selected destination is ineligible. For a pending email destination, ask the recipient to confirm the address and reapply.

## Verify

`go test ./...` runs the deterministic HTTP tests. To run CLI acceptance, use a disposable Irisbeat v0.5.0 installation with `MIN_CHECK_INTERVAL_SECONDS=120`. Inject its Write API key as `IRISBEAT_TEST_API_KEY` and the installation secret for independent Operator edits as `IRISBEAT_TEST_INSTALLATION_SECRET`. The tests create and delete Monitors and Notification destinations. For the email lifecycle and cutover tests, configure [email delivery](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/install.md#optional-email-confirmation) with local mail capture. Set `IRISBEAT_TEST_EMAIL=1` and set `IRISBEAT_TEST_MAIL_CAPTURE_URL` to the Miniflare Local Explorer email sending endpoint for the delivery Worker. Set `NOTIFICATION_PUBLIC_ORIGIN` on the monitoring Worker to the same loopback origin as `IRISBEAT_TEST_BASE_URL` so the tests can submit the captured confirmation form.

```sh
export IRISBEAT_TEST_BASE_URL=http://127.0.0.1:8787
export IRISBEAT_TEST_PROVIDER="$provider_dir/terraform-provider-irisbeat"
export IRISBEAT_TEST_EMAIL=1
export IRISBEAT_TEST_MAIL_CAPTURE_URL="$IRISBEAT_TEST_BASE_URL/cdn-cgi/local/explorer/api/local/email/sending?worker=irisbeat-delivery"
IRISBEAT_TEST_CLI="$(command -v terraform)" go test ./acceptance -count=1 -v
IRISBEAT_TEST_CLI="$(command -v tofu)" go test ./acceptance -count=1 -v
```

See [Release and registry verification](docs/release.md) for signing, publisher enrollment, and clean registry installs.

## License

Copyright 2026 Webriot. This project's own code is open source under [AGPL-3.0-only](LICENSE). Third-party dependencies retain their own licenses. See [NOTICE](NOTICE).
