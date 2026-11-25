---
page_title: "Irisbeat Provider"
description: "Manage Monitors and Notification destinations on self-hosted Irisbeat."
---

# Irisbeat Provider

The provider manages HTTP, TCP, and Heartbeat Monitors and webhook and email Notification destinations using the [Irisbeat v1 automation API](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/api-v1.md).

| Component | Tested minimum |
| --- | --- |
| Self-hosted Irisbeat | v0.5.0 |
| Terraform | v1.14.0 |
| OpenTofu | v1.12.5 |

```hcl
terraform {
  required_providers {
    irisbeat = {
      source  = "webriot/irisbeat"
      version = "~> 0.1.0"
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

[Issue a Write API key](https://github.com/Webriot/irisbeat/blob/v0.5.0/docs/api-v1.md#issue-a-key) on the installation. Set `IRISBEAT_BASE_URL` to its origin and inject the returned key as `IRISBEAT_API_KEY` through your secret store. Run `terraform init` or `tofu init`.

## Provider configuration

- `base_url` (String, optional) uses `IRISBEAT_BASE_URL` when omitted.
- `api_key` (String, sensitive, optional) uses `IRISBEAT_API_KEY` when omitted. Supply a Write key for the installation at `base_url`.

Use aliases with their own configuration for additional installations. Restrict access to plan and state files, which can retain sensitive values.
