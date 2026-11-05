# Docker Sandboxes machine provider for Devsy

This provider gives each Devsy machine one local Docker Sandboxes microVM. Devsy runs its Linux agent and built-in Docker driver inside that VM. A workspace devcontainer, its builds, Features and Compose services use the VM's private Docker Engine, not the workstation's Docker Engine.

**Release status:** The reviewed compatibility profile currently lists macOS arm64 with `sbx` v0.50.1. There is no published provider manifest or supported host claim. A successful provider build and the `sbx` client reporting its version do not establish that a VM can start. The release generator accepts only hosts listed in the reviewed profile. Publication remains gated on a signed provider tag, compatible published Devsy core release, successful native VM jobs, reviewed evidence and the protected `provider-release` environment. Do not install an unreleased build as a supported provider.

## Getting started

The initial qualification targets are macOS 14 or later on Apple silicon with platform virtualization, Windows 11 amd64 with Windows Hypervisor Platform enabled, and Ubuntu 24.04 or later on amd64 or arm64 with accessible KVM. A VM host also needs nested virtualization. These are qualification targets, not evidence of support. Intel macOS, Windows arm64, WSL and other Linux distributions are not advertised.

After a release passes native VM qualification for your exact host, install the standalone [`sbx` executable](https://docs.docker.com/ai/sandboxes/install/) through Docker's host-specific instructions. Sign in using Docker's own workflow outside Devsy. Configure the required [network policy](https://docs.docker.com/ai/sandboxes/configuration/settings/) using Docker's tooling. The provider does not install Docker, sign you in, modify your policy or change a host Docker daemon.

Install a compatible Devsy core release first. The release compatibility profile states its minimum version and the supported `sbx` client and daemon versions. Then install the provider:

```sh
devsy provider add docker-sandbox
devsy workspace up https://github.com/devcontainers/samples --provider docker-sandbox
```

A local folder is uploaded through Devsy's remote machine source flow. It is not mounted from the workstation into the sandbox. Git-backed workspaces are cloned into the guest and keep edits in the guest until you push or explicitly export them. Use the normal Devsy SSH, editor and forwarding commands to access the final devcontainer. Publishing a Compose port inside the VM does not by itself open a workstation port.

## Configuration

Set resource options before creating a machine:

```sh
devsy provider set docker-sandbox -o SBX_CPUS=4 -o SBX_MEMORY=8g -o SBX_ROOT_DISK=60g -o SBX_DOCKER_DISK=80g
```

`SBX_PATH` selects an executable on `PATH` or an absolute executable path. CPUs are a positive integer up to the host logical CPU count. Memory and disk values use binary `m` or `g` units; memory is at least 512 MiB, root disk at least 8 GiB and Docker disk at least 512 MiB. Resources are immutable for an existing machine. To change them, create a new machine, move the data using Git or explicit export, verify the new machine and then delete the old machine.

Normal stop/start preserves the VM's identity, source, containers, images and volumes. Rebuilding a devcontainer preserves the outer VM. Deleting a default workspace with its automatically deleted machine removes **all** guest data, including Docker named volumes, even without `--remove-volumes`. Deleting a workspace attached through an explicit shared machine retains that machine and other workspaces. Removing the provider installation does not remove its VMs or external recovery journals.

In `--single-machine` mode, deleting the last workspace also deletes the shared VM and all guest data. Back up anything you need before deletion.

### Security and network authority

The provider creates a mountless sandbox and does not provide the host project directory to `sbx`. That does not remove authority already granted by Docker Sandboxes network policy, registry credentials, proxy, host SSH agent or MCP integrations. Devsy may independently forward Git and Docker credentials, SSH and signing agents or configured secrets. Review both systems' settings. Do not assume clearing the client `SSH_AUTH_SOCK` disables a daemon-configured integration. The provider does not suppress or reconfigure host integrations.

The guest Docker socket belongs to the VM. Devsy does not install a missing Docker CLI or modify the VM daemon. The agent requires `docker`, Buildx, Compose and the Unix socket at `/var/run/docker.sock` to be present and working. A missing tool is an error, not a request to install it or use a workstation Engine. Docker's allowed HTTPS access still depends on the configured sandbox policy. Builds and containers have their own environment and CA stores; the provider does not inject a universal proxy or CA and never disables TLS verification.

Projects that need authenticated forward-proxy HTTPS must explicitly read the VM's proxy settings, decode its public proxy CA to a generated file outside source control, add that file to the project build context, install it alongside existing roots **before** the first networked build step, and pass HTTP, HTTPS and no-proxy build arguments. Set container runtime proxy variables and install trust in the target image or application as appropriate. Rebuild when the CA rotates. Debian and Ubuntu use their system CA update tooling; Alpine, Java and other applications can require different trust stores. A full project recipe must be qualified on a real guest before being presented as tested. Template image pulls use Docker Sandboxes registry configuration; Devsy's separate guest Docker credential forwarding governs private devcontainer pulls.

### Diagnostics and recovery

Use `devsy provider list --result-format=json` to identify the installed alias and inspect its `config.binaries.DOCKER_SANDBOX_PROVIDER` entry for the native asset name. The installed executable is under Devsy's data directory at `contexts/<context>/providers/<alias>/binaries/docker_sandbox_provider/<asset-name>`. If you installed with `DEVSY_HOME` set, that is the data directory; otherwise use Devsy's OS-specific data directory, where its `config.yaml` lives. Do not use the asset URL as a filesystem path or assume the executable is globally on `PATH`. Run the resolved executable directly:

```sh
/path/to/installed/provider/binary doctor --machine-key KEY --json
```

Doctor reads the external journal and scoped backend observations without booting a stopped VM, uploading diagnostics or printing credentials. The machine key is the exact key printed in a provider failure or journal record. It reports client/daemon compatibility, the recorded sandbox name and immutable ID, and the observed state. An unavailable inventory is not proof the machine disappeared.

The provider retries a transient inventory and inspection disagreement once. It never treats a changed immutable ID, a host workspace mount, or an unqualified state as a missing machine.

Every guest command runs with a private session variable named after its journaled attempt. When Devsy cancels a command, the provider stops the local `sbx` client and then ends every guest process that still carries that session variable. Other sessions and the VM keep running. A guest process that clears its environment can outlive cancellation. If the provider cannot confirm the guest cleanup it reports `E_SESSION_UNTERMINATED` with the session identifier.

If creation fails, diagnose the exact journal key. Resolve an unclean child attempt or uncertain backend operation before retrying. A matching existing VM resumes readiness. A resource that disappeared after its immutable ID was stored is missing data and is never recreated automatically. A failed Devsy workspace creation can remove the machine registration while leaving the provider's journal. In that case, use the exact `devsy machine create` command rendered by doctor with the **recorded** machine ID, context, current alias and resource values; verify its machine folder matches the journal; then repeat the original workspace up command. Do not substitute current option defaults.

If an owned `sbx` client was orphaned, terminate that exact process or restart the host before acknowledging only its attempt:

```sh
/path/to/installed/provider/binary recover --machine-key KEY --attempt ATTEMPT_ID --acknowledge-client-terminated
```

Independently resolve the uncertain Docker-side operation and confirm it has ended before acknowledging its exact transaction:

```sh
/path/to/installed/provider/binary recover --machine-key KEY --operation OPERATION_ID --acknowledge-backend-settled
```

Never erase the journal, reset all sandboxes or delete a sandbox by name alone.

After uninstalling Devsy's machine registration or force deleting it, reinstall the same provider family and use the persisted key:

```sh
/path/to/installed/provider/binary delete --machine-key KEY
```

Deletion still checks the qualified `sbx` profile, ownership ID, active sessions and verified absence. If those checks fail, retain the journal for recovery. Host-wide `sbx reset` and global prune are never recovery instructions for this provider.

## Development

Run `mise run test`, `mise run lint` and `mise run build` for ordinary development. Native VM suites require authenticated, virtualization-capable runners. A build or client version response does not qualify a host.

## Release

Release Please opens a version and changelog pull request after main CI passes. It does not create a tag. After the release pull request is merged, the operator creates a signed provider tag on that exact merge commit. Because Release Please skips creating a GitHub release, remove its pending label and mark the release tagged so it can open later release pull requests:

```sh
gh pr edit <n> --remove-label "autorelease: pending" --add-label "autorelease: tagged"
```

Dispatch the release workflow from the signed tag with a published compatible Devsy core release and its verified commit SHA:

```sh
gh workflow run release.yml --ref vX.Y.Z -f version=vX.Y.Z -f core_ref=<core sha> -f core_tag=<core tag>
```

The workflow requires `GITHUB_REF` to equal `refs/tags/$VERSION`. Distribution builds and tests provider executables, generates the manifest and checksums, and verifies installation through an alternate alias. On the first dispatch, native VM jobs run, then the evidence job fails because `SBX_NATIVE_EVIDENCE` must cite a successful `release.yml` native VM job for the same candidate commit. Review the run, assemble the evidence JSON using the successful native VM job URLs, set `SBX_NATIVE_EVIDENCE` on the `native-sandbox-qualification` environment, then dispatch the workflow again with the same tag and core inputs. After native jobs and evidence validation pass, approve the protected `provider-release` environment. Publication occurs only after those gates pass.

The workflows read these repository variables:

- `SBX_DISTRIBUTION_CORE_REF` selects the pinned Devsy core commit for CI distribution checks.
- `SBX_NATIVE_RUNNER_MACOS_ARM64`, `SBX_NATIVE_RUNNER_WINDOWS_AMD64`, `SBX_NATIVE_RUNNER_LINUX_AMD64`, and `SBX_NATIVE_RUNNER_LINUX_ARM64` provide JSON runner label arrays for qualified hosts.
- `SBX_NATIVE_EXECUTABLE` identifies the checksum verified `sbx` executable on native runners.
- `SBX_NATIVE_DENIED_URL` identifies a reachable URL denied by the configured sandbox policy.
- `SBX_NATIVE_EVIDENCE` supplies reviewed evidence for the exact provider and core commits. It must reference successful native VM jobs from the release workflow run for that candidate.

### Native qualification

Each advertised host requires a virtualization-capable runner with the exact OS and architecture, a pinned `sbx` installation, an authenticated Docker Sandboxes account and network policy, the compatible Devsy core executable, and a reviewed compatibility profile backed by captured native CLI and daemon outputs. Native validation must establish identity and inventory states, mountless creation, detached persistence, byte transport, cooperative cancellation, root and Docker disk sizing, fixed guest socket and Docker features, VM ownership and crash recovery, network policy and secrets, installer checksum rejection, and real Devsy up, exec, SSH, IDE, forwarding, Compose, stop/start, rebuild and deletion. An unavailable runner or missing sign-in blocks that tuple. Do not claim native support on the strength of unit tests.

The native workflow requires JSON runner label arrays for each qualified host, using `SBX_NATIVE_RUNNER_MACOS_ARM64`, `SBX_NATIVE_RUNNER_WINDOWS_AMD64`, `SBX_NATIVE_RUNNER_LINUX_AMD64` or `SBX_NATIVE_RUNNER_LINUX_ARM64` as appropriate. Configure only tuples listed in the reviewed profile. Each approved native runner needs `SBX_NATIVE_EXECUTABLE` resolving the checksum-verified pinned standalone executable, working platform virtualization, a prior Docker Sandboxes sign-in and an effective network policy suitable for allowed test destinations. Set `SBX_NATIVE_DENIED_URL` to a reachable destination expressly denied by that policy; a generic network failure does not prove denial. Do not place login tokens in a fork pull request or print them in CI. The protected `native-sandbox-qualification` environment must supply `SBX_NATIVE_EVIDENCE`, a reviewed JSON report for the exact candidate and core commits. It lists each qualified host, OS version, client version and revision, daemon schema and version, Docker server, Buildx and Compose versions, a successful native CI **job URL** for that same commit, and passing results for every measured gate named in the workflow. The gate checks job conclusions through GitHub Actions. The protected `provider-release` environment approves publication after the native and package lanes pass. A signed provider tag and an already published compatible, signed Devsy core release are prerequisites.

The native test proves denial in two parts. The guest must fail to reach `SBX_NATIVE_DENIED_URL`, and `sbx policy check network` for the new sandbox must report the destination as denied while reporting the allowed build destination as allowed. A guest network failure alone is never accepted as policy evidence.

The evidence URL must identify the matching host's successful native VM job in the release workflow. CI checks its virtualization prerequisite and workspace exercise steps before publication.

The required Devsy core release is either a stable release or a published prerelease. The provider compares versions by semantic version precedence, so a prerelease is older than its stable release and a development build is never accepted. When the pinned core is a prerelease, the workflow publishes the provider release as a prerelease.

Capture sanitized native CLI outputs with provenance under `internal/sbx/testdata/<sbx-client-version>/` only after executing them on a real host. That directory's `provenance.json` must list at least four actual captured `.json` or `.txt` files as `captures` entries with `file`, `sha256` and `kind` fields. Include separate `failure`, `stop_result` and `rm_result` captures; the failed command must not be turned into an invented inventory state. Successful stop and remove must have measured postconditions, not an assumption that exit zero means success. The reviewer supplies the provenance file's SHA-256 in each host's `fixture_provenance_sha256` evidence field and records the measured operation result schema in the profile and host evidence. CI verifies provenance and each named output before release. Do not create synthetic daemon, inventory, settings or operation result fixtures.

Each host evidence entry must also name the measured `shell_setting_schema` and `disk_sizing_schema` from the reviewed profile. The current disk probe allows at most three percent filesystem overhead and requires separate root and Docker filesystems with free space. This bound was measured on macOS arm64 with `sbx` v0.50.1; it does not qualify any other host or version. Capture actual guest capacity and device observations before adding a host to the profile.

`mise run test` and `mise run lint` cover ordinary development. Native package installation uses the real Devsy parser and downloader, including an alternate alias and corrupt-checksum rejection. The release generator hashes actual executable bytes and includes only qualified hosts. Neither the provider nor its releases redistribute `sbx` or logged-in credentials.
