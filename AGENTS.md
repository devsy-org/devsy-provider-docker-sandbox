# AGENTS.md

---

## Environment Setup

This repository is a Devsy machine provider backed by the standalone Docker Sandboxes `sbx` executable. The provider owns the outer microVM and its journal. Devsy owns the guest agent, the Docker driver and the inner devcontainers. Do not replace the Docker driver and do not call undocumented sandboxd interfaces.

### Prerequisites and Tooling

Toolchain is managed by mise. Install mise using `curl https://mise.run | sh`. Install the toolchain (Go, golangci-lint and prek, pinned in `mise.toml`) with `mise install`. Then run `mise run setup` to download Go modules and install the git hooks.

Running the provider against real sandboxes needs the standalone `sbx` executable, signed in. Devsy `v1.27.0` or newer is required.

---

## Common Developer Commands

`mise tasks` shows developer commands. The tasks:

- **Setup**: `mise run setup` runs `mise run setup:dependencies` (Go modules, with retries) and `mise run setup:hooks` (installs the prek git hooks).
- **Lint**: `mise run lint` runs `golangci-lint run` and checks formatting.
- **Format**: `mise run format` applies Go formatting.
- **Run unit tests**: `mise run test` runs `go test -race ./...` and `go vet ./...`.
- **Build development binary**: `mise run build` writes `dist/devsy-provider-docker-sandbox`.
- **Run all hooks**: `mise run pre-commit` runs every prek hook on all files.
- **Qualify**: `mise run qualify:native` runs the native suite on this host and writes local qualification evidence. See Native Tests.
- **Package**: `RELEASE_TAG=<tag> mise run package` generates the checksum-pinned manifest from executables already placed in `dist`. It requires executables for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64. It requires measured disk sizing evidence for every host listed in `qualified_hosts` and rejects evidence for a host that is not listed.

---

## Testing

### Unit Tests

`mise run test` is the unit suite. CI runs the same commands on Linux amd64, Linux arm64, macOS and Windows, then builds and runs the provider binary on each host. Tests use the standard `testing` package. The repository has no third party test dependency.

### Native Tests

Native tests are in `integration/` and carry the `native` build tag. They drive real Devsy workspaces on a real `sbx` microVM. A successful build, a unit test run or a client only `sbx version` is not native qualification.

Native qualification is local by design. GitHub hosted runners cannot run `sbx`, which needs virtualization, and the project registers no self-hosted runner. The maintainer runs the suite on an Apple Silicon Mac and provides reviewed evidence to the release workflow. Release notes say that hosts are qualified locally by the maintainer, not by CI.

Run it before each release and whenever a new `sbx` version lands. Check out the candidate commit with a clean tree, then run:

```bash
SBX_PATH=<qualified sbx> CANDIDATE_COMMIT=<sha> CORE_REF=<core sha> CORE_TAG=<core tag> RELEASE_TAG=<provider tag> SBX_NATIVE_DENIED_URL=<denied url> mise run qualify:native
```

The task checks the tree and `sbx` with `go run ./cmd/qualify-native preflight`, builds the pinned Devsy core and the cancel helper, cross compiles the four provider executables, packages them and runs `go test -tags=native ./integration -run '^TestNative' -count=1 -v -timeout=75m`. It tees the full log, then `go run ./cmd/qualify-native write` writes `dist/native-qualification/<candidate>/native-evidence.json`. The writer refuses when any native test failed or was skipped, when the tree is dirty, when HEAD is not the candidate commit, when the host is not in `qualified_hosts`, or when the `sbx` client, revision or daemon does not match `internal/compat/profile.json` before or after the run. Each host entry carries a `local_run` record with the SHA-256 of the log, the start and end times and a hash of the host name. Keep the log with the evidence. Preflight, evidence writing and verification require at least four fixture captures, including failure, stop and remove results, and verify every recorded file checksum.

To prove the pipeline without the full suite, set `QUALIFY_TEST_OUTPUT_SELECTION` to a narrower `-run` pattern. The writer then labels the file `native-evidence.test-output.json`, sets `local_run.kind` to `local-test-output` and marks every gate `not_run`. `verify` always rejects such a file, so it can never be stored as `SBX_NATIVE_EVIDENCE`. The writer takes the qualified `sbx` client, revision and daemon from `internal/compat/profile.json`, so qualifying a new `sbx` version means updating the profile and fixtures first, then running the task.

Store reviewed native evidence only in the `SBX_NATIVE_EVIDENCE` repository variable. It may be supplied before or after the release PR merges. The `evidence` job in `.github/workflows/e2e.yml` verifies it for releases. The local build is a cross compile of the same sources, so it is not byte compared with the CI artifact.

The tests read these environment variables, which `qualify:native` sets: `DEVSY_NATIVE_CORE_BIN`, `DEVSY_NATIVE_CORE_SOURCE`, `DEVSY_NATIVE_RELEASE_DIR`, `DEVSY_NATIVE_VERSION`, `DEVSY_NATIVE_CANCEL_HELPER`, `SBX_PATH` and `SBX_NATIVE_DENIED_URL`. A test that needs one of them fails when it is missing. The workspace test logs the guest Docker, buildx and Compose versions on a `native-evidence` line that the writer reads.

The `concurrency_crash` evidence needs concurrent guest sessions and `TestNativeProviderCrash`. The test builds the provider, records a real `sbx` start, observes the contender's main goroutine waiting with the mutation lock held by the first provider, and kills the first provider. The contender must then finish without changing the interrupted journal. Recovery must mark uncertainty, settle the backend, restore readiness and verify deletion against inventory. Unit tests do not replace this evidence.

### Qualified Combination

`internal/compat/profile.json` is the source of truth:

- `sbx` client `v0.51.0`, revision `7d55286de0c775148b985ed349f760f1eba06203`, daemon `v0.51.0`.
- Qualified host: `darwin/arm64`.
- Minimum Devsy core: `v1.27.0`.

Every other host is built and unit tested only. It still runs, with one warning that says so, and `doctor` reports the same warning. Only the host check and the per-host disk sizing check depend on `qualified_hosts`.

The qualified version is the latest stable `sbx` release. Pre-releases are not qualified.

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
- Stop and delete send the `sbx` command and verify the outcome by reading the inventory again. Delete requires the sandbox to be absent. Stop accepts only a listed stopped state. Any other state, including an unlisted one, keeps waiting and marks the operation uncertain at the timeout.
- Status, create and start stay fail closed on an unrecognized state. Devsy needs a known status token, and a start must not build on a state the provider cannot classify.
- The `command` verb never prints a version or state warning, so the guest stream stays clean.
- Devsy surfaces provider standard error from create and start at info level. A create followed by start prints the unqualified host or unknown version warning once per verb. There is no deduplication across processes by design.

Report a required daemon restart with an actionable error and never restart it automatically.

Never infer absence from a failed inventory. Never delete by name without matching the persisted immutable sandbox ID. Keep host Docker and host project mounts outside the provider contract. Do not commit credentials or local journal files.

### Disk Sizing

The configuration minimums come from measured guest capacity on the qualified combination, not from `sbx` limits. The guest filesystem is 2.3 to 5 percent smaller than the request. `SBX_DOCKER_DISK` below 7g (measured: 6g fails at a 3.001 percent shortfall, 6.5g passes by a thin margin) and `SBX_ROOT_DISK` below 8g would fail the 3 percent capacity probe.

- `init` and `create` admit a new sandbox spec. They reject sizes below the minimums with `E_CONFIG_DRIFT`, naming the minimum.
- Verbs that act on an existing machine (start, stop, delete, status, describe, command, recover) validate the size syntax and skip the minimums, so a machine created at a smaller size can always be stopped and deleted. `checkSpec` still compares the configured sizes with the recorded ones.
- A size that fails the syntax check names the accepted form: a lowercase `m` or `g` suffix, such as `8g` or `7680m`.
- A create that fails the guest capacity probe returns `E_GUEST_CAPABILITY`. It names the failing disk, the requested and measured bytes, the shortfall percent and a larger size to try, without promising it passes.
- The 3 percent bound is unchanged. Raise a minimum only with new measurements, and never loosen the bound.

### Windows State Security

- State directories and files use an explicit protected DACL at creation. It grants full control only to the current process user, SYSTEM and BUILTIN\Administrators. Existing state must have no allow entries for other principals.
- Reparse points and symlinks are rejected. The owner must be the process token user. BUILTIN\Administrators is also accepted when the process is elevated and the token default owner is BUILTIN\Administrators.
- State files and directories are opened with reparse point traversal disabled, and the opened handle is validated before use. Provider state ancestors are held open without delete sharing during path operations, so they cannot be swapped for junctions.
- The pinned `x/sys` `GetSecurityInfo` wrapper frees its native security descriptor and returns a copy managed by Go.

---

## Code Style and Quality

### Go Code Style

- **Idiomatic**: Prefer simple functions, explicit errors, and clear ownership of processes, streams and temporary files.
- **Style Guide**: Use the Uber style guide https://github.com/uber-go/guide/blob/master/style.md.
- **No code comments**: Do not add comments of any kind, including `//`, `/* */` and lint suppressions such as `nolint` or `nosec`. Go compiler directives such as the `//go:build native` constraint on native test files are required and stay. Names and structure carry the meaning. The `revive` rules `exported` and `package-comments` are disabled for this reason.
- **Process handling**: Keep literal process arguments, byte preserving streams, guest exit codes, cancellation acknowledgement and durable child ownership intact.
- **Logging**: Do not log raw commands, credentials or environment dumps.
- **Linter**: `golangci-lint` via `mise run lint`. Apply formatting with `mise run format`. Run `mise run lint` before pushing changes.

### Documentation

Write documentation in plain English, with simple sentences. Do not join sentences with dashes. Do not put tracker identifiers in repository content.

---

## Pull Request and Commit Guidelines

1. **Contributor License Agreement (CLA)**: All contributors sign the CLA by commenting when asked. Bots are allowlisted. The CLA Assistant check must pass.
2. **Commit messages**: Conventional Commits, with a concise subject line.
3. **Commit signing**: All commits are required to be signed.
4. **Branch name**: Name the branch according to the task.
5. **Pre-commit checks**: Linters and relevant unit tests run before pushing. `prek` manages the hooks installed by `mise run setup`.
6. **Required checks**: The `Lint` and `CI Success` jobs from `ci.yml` must pass.
7. **Reviews**: Wait for the CodeRabbit review and resolve significant valid findings before merge. A skipped or rate limited review remains pending.
8. **Merging**: Merge only with human authorization. Use squash merge with a single line Conventional Commit subject and an empty body.
9. **Release pull requests**: Release Please opens the release pull request, which changes only `.release-please-manifest.json` and `CHANGELOG.md`. After `CI Success`, the `release-please` job in `ci.yml` enables auto merge on it, squashed with the pull request title and an empty body. No human merge step is needed for it.

### Releasing

The release workflow resolves the verified core commit from the published release named by `minimum_devsy_version` in the profile. `SBX_DISTRIBUTION_CORE_REF` configures the CI distribution check only. Release PRs auto merge after CI. Merging creates a draft release and tag, and `Release assets` verifies the commit, core release, distribution and native evidence before publishing. Evidence for the release version can be stored before the merge, but it normally does not exist yet. A failed run never publishes, so a draft and tag left by a failed run are expected.

To recover, check out the release commit's parent and run `mise run qualify:native` with the release tag as `RELEASE_TAG`, the parent as `CANDIDATE_COMMIT`, and the published core tag and its verified commit as `CORE_TAG` and `CORE_REF`. The evidence version must equal the tag. Only modifications to `.release-please-manifest.json` and `CHANGELOG.md` may follow the candidate. Store reviewed evidence only in the `SBX_NATIVE_EVIDENCE` repository variable, then dispatch `Release assets` with the tag. An already published release is verified without uploading again. If a release is abandoned, delete its draft and tag only with `gh release delete <tag> --cleanup-tag`.

The release publishes all four hosts. Release notes and the README list qualified hosts and hosts built and unit tested only.
