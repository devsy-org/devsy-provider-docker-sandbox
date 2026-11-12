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

## License

[MPL-2.0](LICENSE).
