# Changelog

## [1.0.1](https://github.com/devsy-org/devsy-provider-docker-sandbox/compare/v1.0.0...v1.0.1) (2026-11-12)


### Documentation

* keep native evidence in the repository variable ([#29](https://github.com/devsy-org/devsy-provider-docker-sandbox/issues/29)) ([fbb920c](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/fbb920c4f7fc2fd6e8fd5aad2bc1b4eaeb05d1a1))

## [1.0.0](https://github.com/devsy-org/devsy-provider-docker-sandbox/compare/v0.1.1...v1.0.0) (2026-11-11)


Natively qualified: darwin/arm64 with sbx v0.51.0. Built and unit tested only: linux/amd64, linux/arm64 and windows/amd64. Hosts without native qualification evidence run with a warning.

### Features

* **release:** publish all supported hosts and warn on unqualified ones ([9498eef](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/9498eef5412c74bf92064b5fb1ba86989d6e2185))
* qualify sbx v0.51.0 on macOS arm64 ([98c3cb9](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/98c3cb9e13a10f36d40c839eac3db8eedaa950c9))


### Bug Fixes

* **release:** verify stop only on a mapped stopped state ([987b5d7](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/987b5d722b7d070638ff8ea6f0d808ef8aa790fe))
* **state:** enforce ownership and access checks on windows ([037c91d](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/037c91d1ee1f0811e0fc90418d5bda34115ca171))
* **sbx:** report a clear error when the daemon needs a restart ([d9e1276](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/d9e127609b20c85b9ca1b36fef6cf7d18ce455c1))
* **config:** reject docker disk sizes the guest cannot satisfy ([be60cfc](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/be60cfc513393b119c9d1a2c14af01a09cf88cfc))
* **config:** enforce resource minimums only when creating ([aeec2d4](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/aeec2d4fb64842bf8da51ecdb7a19845a51039f2))
* **sbx:** drop sbx output after the provider cancels an exec ([e4d51e2](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/e4d51e28910e2084c701d1a6ccf4f3484f50f698))
* **config:** explain capacity and size errors ([c7e5982](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/c7e5982ae46839fa7ecefd3e158799bb0e0e5f3a))


### Documentation

* trim the readme and link the website docs ([27c2d29](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/27c2d2944ce9a15462c08bd1d3f9abd86b0412cb))
* **readme:** add the provider init step to the install ([c6d4f5d](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/c6d4f5d92340940070b502c05fd9966484d6334f))
* add MPL-2.0 license ([970e7b6](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/970e7b6eca4bc0ee30d04e6b53f505b1cd108c90))
* align AGENTS.md with core repository ([6cdb619](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/6cdb619c1f3c423b5fd3f1269973c69119160368))


### Continuous Integration

* add the contributor license agreement check ([dc11a97](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/dc11a9780ac0baac7227eaf28a4a8a24131179f6))
* fix the cla action pagination for pull requests with many commits ([72efa03](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/72efa03cd304cce15ac02e44c180e6df49616a71))
* **cla:** bump the CLA action to v2.6.6 ([69a838d](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/69a838d3cce6c7dbb17347434d7bd178218950d8))
* **cla:** bump the CLA action to v2.6.7 ([6b648b9](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/6b648b9ae010ed0390ba692c82e5cfd3c4bb3b32))
* qualify native sbx hosts locally instead of on self-hosted runners ([14a8972](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/14a8972981d1e2ee8bf6f66d5d2cf60038c5edff))


### Maintenance

* align tooling, CI and release with the provider template ([7d82b0b](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/7d82b0b4f4d45309238a6e039e98fe2f8f77fe78))
* **release:** set the next release to 1.0.0 ([79429cd](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/79429cdf909bb439c4bcaba69f4785b0a66119c5))


### Tests

* **native:** crash the provider mid-operation against a real sbx ([1889e9e](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/1889e9e2c441e4fdd6b23f31733569044c818b5f))
* **native:** read stdout and stderr separately in the crash test ([590892d](https://github.com/devsy-org/devsy-provider-docker-sandbox/commit/590892d78c538b9bab036973c1b2e7c216285777))

## 0.1.1 (2026-11-04)

### Bug Fixes

* Set the sbx path with provider set in native tests.

## 0.1.0 (2026-11-04)

### Features

* Add the Docker Sandboxes machine provider.
