# Provider engineering

This repository implements a Devsy machine provider backed by the standalone Docker Sandboxes `sbx` executable. The provider owns the outer microVM and its journal. Devsy owns the guest agent, Docker driver and inner devcontainers. Do not replace the Docker driver or manipulate undocumented sandboxd interfaces.

Use the Go version in `go.mod`. Run `mise run test`, `mise run lint` and `mise run build` after a complete change. `mise run package` requires a reviewed, qualified compatibility profile and actual native executable artifacts; it must fail while qualification remains incomplete. Run native VM suites only on authenticated, virtualization-capable runners. A successful Go build or a client-only `sbx version` response is not native qualification.

Keep literal process arguments, byte-preserving streams, guest exits, cancellation acknowledgement and durable child ownership intact. Never infer absence from a failed inventory or delete by name without matching the persisted immutable sandbox ID. Do not log raw commands, credentials or environment dumps. Keep host Docker and host project mounts outside the provider contract. Do not add code comments. Write documentation in proper English without dashes joining sentences. Do not put tracker identifiers in repository content.

The release generator uses actual executable SHA-256 hashes and lists only hosts explicitly present in reviewed compatibility metadata. Every listed tuple must have successful native installer, VM and workspace evidence for the exact candidate. Never publish an unqualified manifest or invent observed output fixtures. Use signed commits and normal protected review and release processes. Do not commit credentials or local journal files.
