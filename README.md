# Docker Sandboxes provider for Devsy

This provider runs Devsy machines in Docker Sandboxes microVMs.

## Requirements

Install and sign in to Docker's standalone `sbx` executable. Devsy `v1.27.0-beta.4` or newer is required.

## Install

For a stable release:

```sh
devsy provider add docker-sandbox
```

Until a stable release exists:

```sh
devsy provider add --use=false --name docker-sandbox https://github.com/devsy-org/devsy-provider-docker-sandbox/releases/download/v0.1.1/provider.yaml
devsy provider init docker-sandbox
```

## Hosts

Natively qualified: darwin/arm64. Built and unit tested: linux/amd64, linux/arm64, windows/amd64. Unqualified hosts run with a warning in builds from `main`. The v0.1.1 release supports only darwin/arm64 and `sbx` v0.50.1.

Customer documentation: [Docker Sandboxes provider](https://devsy.sh/docs/managing-providers/docker-sandbox).

Contributors: [AGENTS.md](AGENTS.md).

## License

[MPL-2.0](LICENSE).
