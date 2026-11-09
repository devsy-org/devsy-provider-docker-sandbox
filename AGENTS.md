# AGENTS.md

---

## Environment Setup

This repository is a Devsy machine provider backed by the standalone Docker Sandboxes `sbx` executable. The provider owns the outer microVM and its journal. Devsy owns the guest agent, the Docker driver and the inner devcontainers. Do not replace the Docker driver and do not call undocumented sandboxd interfaces.

### Prerequisites and Tooling

Toolchain is managed by mise. Install mise using `curl https://mise.run | sh`. Install the toolchain (Go, golangci-lint and prek, pinned in `mise.toml`) with `mise install`. Then run `mise run setup` to download Go modules and install the git hooks.

Running the provider against real sandboxes needs the standalone `sbx` executable, signed in. Devsy `v1.27.0-beta.4` or newer is required.

---

## Common Developer Commands

`mise tasks` shows developer commands. The tasks:

- **Setup**: `mise run setup` runs `mise run setup:dependencies` (Go modules, with retries) and `mise run setup:hooks` (installs the prek git hooks).
- **Lint**: `mise run lint` runs `golangci-lint run` and checks formatting.
- **Format**: `mise run format` applies Go formatting.
- **Run unit tests**: `mise run test` runs `go test -race ./...` and `go vet ./...`.
- **Build development binary**: `mise run build` writes `dist/devsy-provider-docker-sandbox`.
- **Run all hooks**: `mise run pre-commit` runs every prek hook on all files.
- **Package**: `RELEASE_TAG=<tag> mise run package` generates the checksum-pinned manifest from executables already placed in `dist`. It requires executables for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64. It requires measured disk sizing evidence for every host listed in `qualified_hosts` and rejects evidence for a host that is not listed.

---

## Testing

### Unit Tests

`mise run test` is the unit suite. CI runs the same commands on Linux amd64, Linux arm64, macOS and Windows, then builds and runs the provider binary on each host. Tests use the standard `testing` package. The repository has no third party test dependency.

### Native Tests

Native tests are in `integration/` and carry the `native` build tag. They drive real Devsy workspaces on a real `sbx` microVM. A successful build, a unit test run or a client only `sbx version` is not native qualification.

Native tests run only through the `Native VM qualification` workflow (`.github/workflows/e2e.yml`), which `release.yml` calls. They need a runner that has working virtualization and an authenticated `sbx`. The workflow takes runner labels from the repository variables `SBX_NATIVE_RUNNER_MACOS_ARM64`, `SBX_NATIVE_RUNNER_WINDOWS_AMD64`, `SBX_NATIVE_RUNNER_LINUX_AMD64` and `SBX_NATIVE_RUNNER_LINUX_ARM64`, and the `sbx` path from `SBX_NATIVE_EXECUTABLE`. The only qualified host today is darwin/arm64, so the macOS arm64 runner is the one that is needed. The repository does not state whether a GitHub hosted runner can run `sbx`. Use a runner registered under the variable above.

The workflow builds the Devsy core at the pinned commit, builds `integration/native_cancel.go` as a helper, then runs:

```bash
go test -tags=native ./integration -run '^TestNative' -count=1 -v -timeout=75m
```

The tests read these environment variables: `DEVSY_NATIVE_CORE_BIN`, `DEVSY_NATIVE_CORE_SOURCE`, `DEVSY_NATIVE_RELEASE_DIR`, `DEVSY_NATIVE_VERSION`, `DEVSY_NATIVE_CANCEL_HELPER`, `SBX_PATH` and `SBX_NATIVE_DENIED_URL`. A test that needs one of them fails when it is missing.

### Qualified Combination

`internal/compat/profile.json` is the source of truth:

- `sbx` client `v0.50.1`, revision `ddb0268d7124b9cf98955d0b08ffd71a0dfcebe4`, daemon `v0.50.1`.
- Qualified host: `darwin/arm64`.
- Minimum Devsy core: `v1.27.0-beta.4`.

Every other host is built and unit tested only. It still runs, with one warning that says so, and `doctor` reports the same warning. Only the host check and the per-host disk sizing check depend on `qualified_hosts`.

### Fixtures

Do not invent observed output fixtures. Fixtures under `internal/sbx/testdata/<version>/` come from real captures, and `provenance.json` records them with checksums.

---

## sbx Compatibility and Safety

An `sbx` client version, client revision or daemon version outside the qualified combination must never block a command, because `sbx` ships faster than the provider can qualify it. Every command that contacts `sbx`, except `doctor`, warns once per process on standard error and proceeds best effort. This includes status, stop, delete and recover, so users can always stop and delete their VMs after an `sbx` update.

- Guest command execution does not warn, because its standard error belongs to the guest.
- `doctor` lists the same warning and reports a compatibility state of `warning`.
- Never write the warning to standard output. Devsy parses it.
- For an unqualified combination, decode `sbx` output leniently: ignore unknown JSON fields and still require every field the provider uses. A qualified combination keeps the exact schema checks.
- Output that cannot be decoded, an immutable ID or ownership mismatch, and a failed guest disk capacity probe stay fail closed for every version.

A sandbox state string that the profile does not list is handled by verb:

- Stop, delete and recover warn once, still require the recorded immutable ID and then proceed.
- Stop and delete send the `sbx` command and verify the outcome by reading the inventory again. Delete requires the sandbox to be absent. Stop accepts a listed stopped state, or an unlisted state that differs from the one observed before the command. While the state is unchanged, stop keeps waiting and marks the operation uncertain at the timeout.
- Status, create and start stay fail closed on an unrecognized state. Devsy needs a known status token, and a start must not build on a state the provider cannot classify.
- The `command` verb never prints a version or state warning, so the guest stream stays clean.

Never infer absence from a failed inventory. Never delete by name without matching the persisted immutable sandbox ID. Keep host Docker and host project mounts outside the provider contract. Do not commit credentials or local journal files.

---

## Code Style and Quality

### Go Code Style

- **Idiomatic**: Prefer simple functions, explicit errors, and clear ownership of processes, streams and temporary files.
- **Style Guide**: Use the Uber style guide https://github.com/uber-go/guide/blob/master/style.md.
- **No code comments**: Do not add comments of any kind, including `//`, `/* */` and lint suppressions such as `nolint` or `nosec`. Names and structure carry the meaning. The `revive` rules `exported` and `package-comments` are disabled for this reason.
- **Process handling**: Keep literal process arguments, byte preserving streams, guest exit codes, cancellation acknowledgement and durable child ownership intact.
- **Logging**: Do not log raw commands, credentials or environment dumps.
- **Linter**: `golangci-lint` via `mise run lint`. Apply formatting with `mise run format`. Run `mise run lint` before pushing changes.

### Documentation

Write documentation in plain English, with simple sentences. Do not join sentences with dashes. Do not put tracker identifiers in repository content.

---

## Pull Request and Commit Guidelines

1. **Contributor License Agreement (CLA)**: All contributors sign the CLA. The CLA Assistant check must pass.
2. **Commit messages**: Conventional Commits, with a concise subject line.
3. **Commit signing**: All commits are required to be signed.
4. **Branch name**: Name the branch according to the task.
5. **Pre-commit checks**: Linters and relevant unit tests run before pushing. `prek` manages the hooks installed by `mise run setup`.
6. **Required checks**: The `Lint` and `CI Success` jobs from `ci.yml` must pass.
7. **Reviews**: Wait for the CodeRabbit review and resolve significant valid findings before merge. A skipped or rate limited review remains pending.
8. **Merging**: Merge only with human authorization. Use squash merge with a single line Conventional Commit subject and an empty body.
9. **Release pull requests**: Release Please opens the release pull request, which changes only `.release-please-manifest.json` and `CHANGELOG.md`. After `CI Success`, the `release-please` job in `ci.yml` enables auto merge on it, squashed with the pull request title and an empty body. No human merge step is needed for it.

### Releasing

Release Please runs with `skip-github-release`, so it opens release pull requests but creates no tag and no release.

1. After the release pull request merges, an operator creates a signed tag on the merged release commit. The tag version must match `.release-please-manifest.json`.
2. Remove the `autorelease: pending` label from the release pull request and add `autorelease: tagged`, so Release Please can continue opening release pull requests.
3. Dispatch the `Qualified provider release` workflow (`release.yml`) from the tag ref. Inputs: `version` (the tag), `core_ref` (the commit SHA of a released compatible Devsy core) and `core_tag` (that core release tag). The workflow checks the signed tag and commit, then builds and tests all four hosts, installs the provider with the pinned core, and runs the native VM jobs for every host in `qualified_hosts`.
4. The first dispatch has no reviewed evidence, so its `evidence` job fails before publication. Review the native VM job results, then record the evidence JSON in the `SBX_NATIVE_EVIDENCE` repository variable. Each host entry links a job of a `release.yml` dispatch run on the same commit.
5. Dispatch the workflow a second time with the same inputs. The `evidence` job validates `SBX_NATIVE_EVIDENCE` against the profile and the recorded runs. The `publish` job then waits for approval in the protected `provider-release` environment and creates the release.

The release publishes all four hosts. Release notes and the README must list which hosts are natively qualified and which are built and unit tested only.
