# Docker Sandboxes provider for Devsy

This provider runs Devsy machines in Docker Sandboxes microVMs.

## Requirements

Install and sign in to Docker's standalone `sbx` executable. Devsy `v1.27.0` or newer is required.

## Install

```sh
devsy provider add docker-sandbox
devsy provider init docker-sandbox
```

## Hosts

Natively qualified: darwin/arm64 with `sbx` v0.51.0. The qualified version is the latest stable `sbx` release. Pre-releases are not qualified. Built and unit tested: linux/amd64, linux/arm64, windows/amd64. Unqualified hosts run with a warning.

Qualification is local by design. GitHub hosted runners cannot run `sbx`, so the maintainer runs `mise run qualify:native` on an Apple Silicon Mac before each release and whenever a new `sbx` version lands. The release notes name each qualified host and give the hash of its test log. Hosts are qualified locally by the maintainer, not by CI.

Customer documentation: [Docker Sandboxes provider](https://devsy.sh/docs/managing-providers/docker-sandbox).

Contributors: [AGENTS.md](AGENTS.md).

## Releases

Release Please opens a release PR for user-facing changes. Before it can merge, the maintainer qualifies its base commit on a Mac and stores reviewed evidence in the `SBX_NATIVE_EVIDENCE` repository variable. Re-run the failed `Release evidence` job or all failed jobs to continue. Re-running only `CI Success` retains the failed evidence result. If the base changes, qualify the new base. After the PR merges, the workflow creates a draft and tag, verifies the evidence again, and publishes the assets. See [AGENTS.md](AGENTS.md) for the qualification inputs and recovery instructions.

## License

[MPL-2.0](LICENSE).
