---
page_title: "irisbeat_monitor Resource"
description: "Manage a saved Irisbeat Monitor and its Notification destination assignments."
---

# irisbeat_monitor

```hcl
resource "irisbeat_monitor" "database" {
  name            = "Database"
  kind            = "tcp"
  hostname        = "db.example.com"
  port            = 5432
  published       = false
  schedule_mode   = "calendar"
  cron            = "*/5 * * * *"
  timezone        = "UTC"
  destination_ids = [irisbeat_notification_destination.alerts.id]
}
```

## Schema

| Argument | Type | Description |
| --- | --- | --- |
| `name` | String, required | Monitor name. |
| `kind` | String, required | `http`, `tcp`, or `heartbeat`. Changing kind replaces the Monitor. |
| `published` | Boolean, required | Show the Monitor on the public status page. |
| `url` | String | HTTP(S) target without embedded credentials. Required for HTTP. |
| `hostname`, `port` | String, number | DNS name or IP and a port from 1 to 65535. Required for TCP. |
| `schedule_mode` | String | `interval` or `calendar`. New Monitors default to Interval. |
| `interval_seconds` | Number | Outbound check interval or expected Interval Heartbeat receipt interval. Irisbeat retains the saved outbound interval in Calendar mode. |
| `timeout_seconds`, `failure_threshold` | Number | Outbound check timeout and consecutive failures to open an Incident. |
| `recovery_threshold` | Number | Consecutive successes to resolve an Incident. |
| `cron`, `timezone` | String | Numeric five-field cron expression and timezone for Calendar mode. Timezone defaults to UTC. |
| `silence_seconds`, `recovery_spacing_seconds` | Number | Time without an Interval Heartbeat receipt before an Incident and minimum spacing between recovery receipts. |
| `grace_seconds` | Number | Calendar Heartbeat receipt grace period. |
| `destination_ids` | Set of strings | Omit to preserve existing routing, set an explicit set to repair routing drift, or use `[]` to clear it. |

Irisbeat supplies omitted timing on create and enforces the installation's check floor. A pending email destination causes Irisbeat to reject the entire Monitor write. Have the recipient confirm the inbox before reapplying.

The resource exports `id` (String) and `heartbeat_path` (String for Heartbeat Monitors). Configure a job to send an HTTPS POST to the installation origin plus `heartbeat_path`. Put the installation secret in the job's secret store and use it as the Bearer credential. See the [Heartbeat receipt API](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/heartbeat-api.md#send-a-receipt).

## Import

Declare matching saved configuration and run `terraform import irisbeat_monitor.database <monitor-id>` or `tofu import irisbeat_monitor.database <monitor-id>`. Omit `destination_ids` to adopt the Monitor while preserving Operator control of its existing routing.
