# AGENTS.md

## Environment and commands

This repository implements a Devsy machine provider backed by the standalone Docker Sandboxes `sbx` executable. The provider owns the outer microVM and its journal. Devsy owns the guest agent, Docker driver and inner devcontainers. Do not replace the Docker driver or manipulate undocumented sandboxd interfaces.

Use the Go version in `go.mod`. Run commands through `mise run`:

- `mise run setup`: install dependencies and git hooks.
- `mise run setup:dependencies`: download Go modules with retries.
- `mise run setup:hooks`: install prek git hooks.
- `mise run lint`: run golangci-lint and check formatting.
- `mise run format`: apply Go formatting.
- `mise run test`: run race-enabled Go tests and `go vet`.
- `mise run build`: build `dist/devsy-provider-docker-sandbox`.
- `mise run package`: generate a checksum-pinned manifest from the native artifacts. The generator requires executables for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64, and requires measured disk sizing evidence for every host listed in `qualified_hosts`.
- `mise run pre-commit`: run all prek hooks.

Native VM suites run only on authenticated, virtualization-capable runners. A successful Go build or a client-only `sbx version` response is not native qualification. A host without native evidence is built and unit tested only. It still runs, with one warning that says so, and `doctor` reports the same warning. Only the host check and the per-host disk sizing admission depend on `qualified_hosts`. Keep every other fail-closed check: sbx version and revision, daemon schema, identity and the guest disk capacity probe.

## Code quality and Go behavior

Follow idiomatic Go. Prefer simple functions, explicit errors, and clear ownership of processes, streams, and temporary files. The `revive` `exported` and `package-comments` rules are disabled to honor the no-comments rule.

Keep literal process arguments, byte-preserving streams, guest exits, cancellation acknowledgement and durable child ownership intact. Never infer absence from a failed inventory or delete by name without matching the persisted immutable sandbox ID. Do not log raw commands, credentials or environment dumps. Keep host Docker and host project mounts outside the provider contract. Do not add code comments.

## Validation and pull requests

The release generator uses actual executable SHA-256 hashes and publishes all four supported hosts. `qualified_hosts` is an evidence label for hosts with native VM evidence and does not gate publication of the other hosts. Publication requires a signed tag, compatible published Devsy core release, successful native VM jobs and reviewed `SBX_NATIVE_EVIDENCE` for every host in `qualified_hosts`, and approval through the protected `provider-release` environment. Release notes and the README must list which hosts are natively qualified and which are built and unit tested only. Do not invent observed output fixtures. Do not commit credentials or local journal files.

Use signed Conventional Commits. Pull requests require applicable CI and completed CodeRabbit and Greptile reviews; resolve significant valid findings before merge. A skipped or rate-limited review remains pending. Merge only with human authorization. Squash with a single-line Conventional Commit subject and an empty body. Apply this rule to release pull requests as well. Release Please does not create tags; an operator creates a signed tag on the merged release commit and dispatches the qualified release workflow. Remove the `autorelease: pending` label and add `autorelease: tagged` after tagging so Release Please can continue opening release pull requests.

Write documentation in proper English without dashes joining sentences. Do not put tracker identifiers in repository content.
