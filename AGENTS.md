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
- `mise run package`: generate a checksum-pinned manifest from the native artifacts. The generator requires executables for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64, requires measured disk sizing evidence for every host listed in `qualified_hosts`, and rejects disk sizing evidence for a host that is not listed.
- `mise run pre-commit`: run all prek hooks.

Native VM suites run only on authenticated, virtualization-capable runners. A successful Go build or a client-only `sbx version` response is not native qualification. A host without native evidence is built and unit tested only. It still runs, with one warning that says so, and `doctor` reports the same warning. Only the host check and the per-host disk sizing admission depend on `qualified_hosts`.

The `concurrency_crash` gate requires concurrent guest sessions and `TestNativeProviderCrash`. The native test builds the provider, records a real `sbx` start, observes the contender's main goroutine waiting with the mutation lock held by the first provider, and kills the first provider. The contender must then finish without changing the interrupted journal. Recovery must mark uncertainty, settle the backend, restore readiness and verify deletion against inventory. Unit tests do not replace this evidence.

An `sbx` client version, client revision or daemon version outside the qualified combination in `internal/compat/profile.json` must never block a command, because `sbx` ships faster than the provider can qualify it. Every command that contacts `sbx`, except `doctor`, warns once per process on standard error and proceeds best effort, including status, stop, delete and recover, so users can always stop and delete their VMs after an `sbx` update. Guest command execution does not warn because its standard error belongs to the guest. `doctor` lists the same warning and reports a compatibility state of `warning`. Never write the warning to standard output, which Devsy parses. For an unqualified combination decode `sbx` output leniently by ignoring unknown JSON fields while still requiring every field the provider uses. A qualified combination keeps the exact schema checks. Output that cannot be decoded, an immutable ID or ownership mismatch and a failed guest disk capacity probe stay fail-closed for every version.

A sandbox state string that the profile does not list is handled by verb. Stop, delete and recover warn once, still require the recorded immutable ID and then proceed. Stop and delete send the `sbx` command and verify the outcome by reading the inventory again. Delete requires the sandbox to be absent. Stop accepts only a listed stopped state. An unlisted state keeps waiting and marks the operation uncertain at the timeout. Status, create and start stay fail-closed on an unrecognized state, because Devsy needs a known status token and a start must not build on a state the provider cannot classify. The `command` verb never prints a version or state warning, so the guest stream stays clean. Devsy surfaces provider standard error from create and start at info level. A create followed by start prints the unqualified host or unknown version warning once per verb. There is no deduplication across processes by design.

The configuration minimums come from measured guest capacity on the qualified tuple, not from `sbx` limits. The guest filesystem is 2.3 to 5 percent smaller than the request, so `SBX_DOCKER_DISK` below 7g (measured: 6g fails at a 3.001 percent shortfall, 6.5g passes by a thin margin) and `SBX_ROOT_DISK` below 8g would fail the 3 percent capacity probe. Both are rejected at load with `E_CONFIG_DRIFT` naming the minimum. Raise a minimum only with new measurements, and never loosen the bound.

## Code quality and Go behavior

Report a required daemon restart with an actionable error and never restart it automatically.

Follow idiomatic Go. Prefer simple functions, explicit errors, and clear ownership of processes, streams, and temporary files. The `revive` `exported` and `package-comments` rules are disabled to honor the no-comments rule.

Keep literal process arguments, byte-preserving streams, guest exits, cancellation acknowledgement and durable child ownership intact. Never infer absence from a failed inventory or delete by name without matching the persisted immutable sandbox ID. Do not log raw commands, credentials or environment dumps. Keep host Docker and host project mounts outside the provider contract. Do not add code comments.

## Validation and pull requests

Contributors sign the CLA by commenting when asked; bots are allowlisted.

The release generator uses actual executable SHA-256 hashes and publishes all four supported hosts. `qualified_hosts` is an evidence label for hosts with native VM evidence and does not gate publication of the other hosts. Publication requires a signed tag, compatible published Devsy core release, successful native VM jobs and reviewed `SBX_NATIVE_EVIDENCE` for every host in `qualified_hosts`, and approval through the protected `provider-release` environment. Release notes and the README must list which hosts are natively qualified and which are built and unit tested only. Do not invent observed output fixtures. Do not commit credentials or local journal files.

Use signed Conventional Commits. Pull requests require applicable CI and completed CodeRabbit and Greptile reviews; resolve significant valid findings before merge. A skipped or rate-limited review remains pending. Merge only with human authorization. Squash with a single-line Conventional Commit subject and an empty body. Apply this rule to release pull requests as well. Release Please does not create tags; an operator creates a signed tag on the merged release commit and dispatches the qualified release workflow. Remove the `autorelease: pending` label and add `autorelease: tagged` after tagging so Release Please can continue opening release pull requests.

Write documentation in proper English without dashes joining sentences. Do not put tracker identifiers in repository content.

Windows state directories and files use an explicit protected DACL at creation, granting full control only to the current process user, SYSTEM and BUILTIN\Administrators. Existing state must have no allow entries for other principals. Reparse points and symlinks are rejected. The owner must be the process token user. BUILTIN\Administrators is also accepted when the process is elevated and the token default owner is BUILTIN\Administrators.

Windows opens state files and directories with reparse point traversal disabled, then validates the opened handle before use. Provider state ancestors are held open without delete sharing during path operations so they cannot be swapped for junctions. The pinned x/sys GetSecurityInfo wrapper frees its native security descriptor and returns a copy managed by Go.
