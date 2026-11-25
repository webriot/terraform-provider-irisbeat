# Irisbeat provider

Manage HTTP and TCP Monitors and webhook Notification destinations on a self-hosted Irisbeat installation with Terraform or OpenTofu. The provider uses the [Irisbeat v1 automation API](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/api-v1.md).

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

Use a DNS name or IP address for the TCP hostname and a port from 1 to 65535. Target and schedule edits preserve the Monitor ID. Changing the kind replaces the Monitor.

Irisbeat supplies omitted timing on create and enforces the installation's check floor. Set `schedule_mode = "calendar"` with `cron` to use a Calendar schedule. Set `schedule_mode = "interval"` and remove `cron` and `timezone` to switch back. Irisbeat retains the saved `interval_seconds` in Calendar mode. Supply a new interval when switching back if the saved interval falls below the check floor.

To adopt a Monitor, declare its matching saved configuration and run `terraform import irisbeat_monitor.website <monitor-id>` or `tofu import irisbeat_monitor.website <monitor-id>` using its ID from the same installation. If an interrupted create leaves the outcome unknown, check Irisbeat and import any existing Monitor before attempting another create.

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

## Manage Monitor routing

Add `destination_ids = [irisbeat_notification_destination.alerts.id]` to a Monitor to manage its complete set of Notification destination IDs. Reordering IDs produces an empty plan. Apply repairs changes made in the Operator when an explicit set is configured.

Omit `destination_ids` to observe routing in state and leave it under Operator control. This also preserves the saved assignments when importing a Monitor, editing other fields, or removing a previously configured set. Set `destination_ids = []` to clear all assignments.

Irisbeat rejects the entire Monitor write if any selected destination is ineligible. For a pending email destination, ask the recipient to confirm the address and reapply.

## Verify

`go test ./...` runs the deterministic HTTP tests. To run CLI acceptance, use a disposable Irisbeat v0.5.0 installation with `MIN_CHECK_INTERVAL_SECONDS=120`. Inject its Write API key as `IRISBEAT_TEST_API_KEY` and the installation secret for independent Operator edits as `IRISBEAT_TEST_INSTALLATION_SECRET`. The tests create and delete Monitors and webhook Notification destinations. To include rejection of pending email assignments, configure [email delivery](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/install.md#optional-email-confirmation) on the disposable installation with a local mail capture binding and set `IRISBEAT_TEST_EMAIL=1`.

```sh
export IRISBEAT_TEST_BASE_URL=http://127.0.0.1:8787
export IRISBEAT_TEST_PROVIDER="$provider_dir/terraform-provider-irisbeat"
IRISBEAT_TEST_CLI="$(command -v terraform)" go test ./acceptance -count=1 -v
IRISBEAT_TEST_CLI="$(command -v tofu)" go test ./acceptance -count=1 -v
```

Tested with Irisbeat v0.5.0, Terraform v1.14.0, and OpenTofu v1.12.5.

## License

Copyright 2026 Webriot. This project's own code is open source under [AGPL-3.0-only](LICENSE). Third-party dependencies retain their own licenses. See [NOTICE](NOTICE).
