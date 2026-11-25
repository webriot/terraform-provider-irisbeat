# Release and registry verification

Use one public GitHub release in [Webriot/terraform-provider-irisbeat](https://github.com/Webriot/terraform-provider-irisbeat) for both registries. The first proposed tag is `v0.1.0`. Confirm the version and the Webriot release signer before creating the tag. The release owner must have access to publish GitHub releases for the repository.

## Prepare the publisher

- **Signing.** Use a Webriot-controlled, unexpired RSA OpenPGP key with signing capability. Approve and record its full fingerprint and keep the private key in the signer's GPG key store or hardware token outside the repository. Export the public key with `gpg --armor --export "$RELEASE_SIGNING_FINGERPRINT"` for both registries. Preserve old public keys if rotating. [Terraform signing key requirements](https://developer.hashicorp.com/terraform/registry/providers/publishing#preparing-and-adding-a-signing-key)
- **Terraform Registry.** Sign in with a GitHub account that is a Webriot admin. Grant the Terraform Registry OAuth app the repository access it requests, including organization approval if required. Under **User Settings → Signing Keys**, add the armored public key to the Webriot namespace. After the signed GitHub release is public, use **Publish → Provider** and select Webriot's `terraform-provider-irisbeat`. Confirm the Registry created an active GitHub webhook subscribed to release events and indexed the version. Use the provider settings resync if the webhook needs repair. [Terraform publishing instructions](https://developer.hashicorp.com/terraform/registry/providers/publishing)
- **OpenTofu Registry.** A provider author with public Webriot GitHub membership submits the armored public key under namespace `webriot` and provider name `irisbeat` on the [New Provider Signing Key form](https://github.com/opentofu/registry/issues/new?template=provider_key.yml). Submit the public repo `Webriot/terraform-provider-irisbeat` through the [New Provider form](https://github.com/opentofu/registry/issues/new?template=provider.yml) once the signed release is available. Use the GitHub issue forms in a browser. Record both submission URLs and confirm that the key and version are indexed before collecting install evidence. [OpenTofu Registry procedures](https://github.com/opentofu/registry/blob/main/PROCEDURES.md)

## Build and verify

Run the [Go and CLI acceptance checks](../README.md#verify) against the released Irisbeat source and tested CLI floors. Use the SHA256SUMS attached to the [Irisbeat v0.5.0 release](https://github.com/Webriot/irisbeat/releases/tag/v0.5.0) to verify its source archive. Record the provider revision, the Irisbeat release, each CLI version and test results. Enable simulated email delivery to exercise confirmation and cutover.

Run the package script on Linux with Bash, Git, Go 1.25+, Python 3, GNU coreutils and GPG. Set `RELEASE_SIGNING_FINGERPRINT` to the selected key's full fingerprint and unlock the key with your normal GPG agent. The script builds from the checked-out commit. Set `scratch` to an absolute workspace scratch directory outside the source checkout.

```sh
version=0.1.0
assets="$scratch/assets"
scripts/package-release "$version" "$assets"
```

This produces Linux, macOS and Windows archives for amd64 and arm64, a shared protocol 6 manifest, a SHA-256 checksum list and a binary detached signature. Each archive includes the provider executable, LICENSE and NOTICE. Both registries discover the same release assets. [Terraform asset and manifest requirements](https://developer.hashicorp.com/terraform/registry/providers/publishing#manually-preparing-a-release), [OpenTofu artifact discovery](https://github.com/opentofu/registry/blob/main/src/internal/provider/version.go)

Verify with a keyring containing only the expected public key. Check its fingerprint against the release owner's record. For a pre-publication rehearsal, use a disposable signing key located outside the source checkout.

```sh
prefix="terraform-provider-irisbeat_${version}"
gpg --export "$RELEASE_SIGNING_FINGERPRINT" > "$scratch/release-public.gpg"
(
  cd "$assets"
  gpgv --keyring "$scratch/release-public.gpg" "$prefix"_SHA256SUMS.sig "$prefix"_SHA256SUMS &&
  sha256sum --check --strict "$prefix"_SHA256SUMS
)
```

To smoke-test the packages before publication, put the same ZIPs in a packed [filesystem mirror](https://developer.hashicorp.com/terraform/cli/config/config-file#explicit-installation-method-configuration) under both `registry.terraform.io/webriot/irisbeat/` and `registry.opentofu.org/webriot/irisbeat/`. In separate fresh directories use the install configuration below and point `TF_CLI_CONFIG_FILE` to a scratch `.tfrc` file containing only this block. Replace the path with the absolute mirror path.

```hcl
provider_installation {
  filesystem_mirror {
    path = "/absolute/path/to/mirror"
  }
}
```

Run `terraform init -backend=false` and `tofu init -backend=false` in their respective directories, followed by `providers schema -json` with each CLI. A successful mirror install and schema load verify the ZIP layout and host binary. The GPG and checksum checks above verify the signature and archive bytes independently.

## Publish after review

1. Check out the approved release commit on the default branch and build the artifacts with the Webriot signing key. Run the build and verification above at that revision. Tag it `v0.1.0` and push the tag. Use [GitHub release creation](https://cli.github.com/manual/gh_release_create) with `--verify-tag --draft` and upload the generated assets together. Confirm the tag commit and assets before publishing the draft as a stable release.
2. Complete first-time registry enrollment. Retain the release URL, tag commit, signed checksum list and full signing fingerprint. Use the same public key and release assets on both registries. For corrections, publish a new version. [OpenTofu version immutability policy](https://github.com/opentofu/registry/blob/main/POLICY.md)
3. Verify direct installs using fresh directories, no existing lock files or plugin cache, and a CLI configuration containing only `provider_installation { direct {} }`. Unset CLI argument overrides and use the tested floors. Pin the version being verified in each directory.

```hcl
terraform {
  required_providers {
    irisbeat = {
      source  = "webriot/irisbeat"
      version = "= 0.1.0"
    }
  }
}
```

Run `terraform init -backend=false -input=false -no-color` in one directory and `tofu init -backend=false -input=false -no-color` in the other. Keep both outputs showing the installed version and verified signing key ID. Check that each lock file names the expected registry host. Check each registry's [package download metadata](https://developer.hashicorp.com/terraform/internals/provider-registry-protocol#find-a-provider-package) for the release and platform. Confirm protocol `6.0`, the expected public key, and a package SHA-256 that matches the same ZIP in the signed release checksum list. Keep both metadata responses and lock files as evidence. [Terraform plugin signing](https://developer.hashicorp.com/terraform/cli/plugins/signing), [OpenTofu plugin signing](https://opentofu.org/docs/cli/plugins/signing/)
