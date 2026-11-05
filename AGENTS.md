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
- `mise run package`: generate a checksum-pinned manifest from qualified native artifacts. The generator accepts only hosts listed in the reviewed compatibility profile.
- `mise run pre-commit`: run all prek hooks.

Native VM suites run only on authenticated, virtualization-capable runners. A successful Go build or a client-only `sbx version` response is not native qualification.

## Code quality and Go behavior

Follow idiomatic Go. Prefer simple functions, explicit errors, and clear ownership of processes, streams, and temporary files. The `revive` `exported` and `package-comments` rules are disabled to honor the no-comments rule.

Keep literal process arguments, byte-preserving streams, guest exits, cancellation acknowledgement and durable child ownership intact. Never infer absence from a failed inventory or delete by name without matching the persisted immutable sandbox ID. Do not log raw commands, credentials or environment dumps. Keep host Docker and host project mounts outside the provider contract. Do not add code comments.

## Validation and pull requests

The release generator uses actual executable SHA-256 hashes and accepts only hosts explicitly present in reviewed compatibility metadata. Publication requires a signed tag, compatible published Devsy core release, successful native VM jobs, reviewed `SBX_NATIVE_EVIDENCE`, and approval through the protected `provider-release` environment. Do not publish an unqualified manifest or invent observed output fixtures. Do not commit credentials or local journal files.

Use signed Conventional Commits. Pull requests require applicable CI and completed CodeRabbit and Greptile reviews; resolve significant valid findings before merge. A skipped or rate-limited review remains pending. Merge only with human authorization. Squash with a single-line Conventional Commit subject and an empty body. Apply this rule to release pull requests as well. Release Please does not create tags; an operator creates a signed tag on the merged release commit and dispatches the qualified release workflow. Remove the `autorelease: pending` label and add `autorelease: tagged` after tagging so Release Please can continue opening release pull requests.

Write documentation in proper English without dashes joining sentences. Do not put tracker identifiers in repository content.
