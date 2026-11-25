# Irisbeat provider

Manage HTTP and Heartbeat Monitors on a self-hosted Irisbeat installation with Terraform or OpenTofu. The provider uses the [Irisbeat v1 Monitor API](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/monitor-api-v1.md).

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
```

Irisbeat supplies omitted timing on create and enforces the installation's check floor. Set `schedule_mode = "calendar"` with `cron` to use a Calendar schedule. Set `schedule_mode = "interval"` and remove `cron` and `timezone` to switch back. Irisbeat retains the saved `interval_seconds` for HTTP Monitors in Calendar mode. Supply a new interval when switching back if the saved interval falls below the check floor.

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

## Verify

`go test ./...` runs the deterministic HTTP tests. To run CLI acceptance, use a disposable Irisbeat v0.5.0 installation with `MIN_CHECK_INTERVAL_SECONDS=120`. Inject its Write API key as `IRISBEAT_TEST_API_KEY` and the installation secret for independent Operator edits as `IRISBEAT_TEST_INSTALLATION_SECRET`. The tests create and delete Monitors.

```sh
export IRISBEAT_TEST_BASE_URL=http://127.0.0.1:8787
export IRISBEAT_TEST_PROVIDER="$provider_dir/terraform-provider-irisbeat"
IRISBEAT_TEST_CLI="$(command -v terraform)" go test ./acceptance -count=1 -v
IRISBEAT_TEST_CLI="$(command -v tofu)" go test ./acceptance -count=1 -v
```

Tested with Irisbeat v0.5.0, Terraform v1.14.0, and OpenTofu v1.12.5.

## License

Copyright 2026 Webriot. This project's own code is open source under [AGPL-3.0-only](LICENSE). Third-party dependencies retain their own licenses. See [NOTICE](NOTICE).
