# Desktop update foundation

Update checks and downloads are disabled in production on every platform. The staged native helper validates signed metadata and bounded downloads; it does not install updates. The desktop release gate and the separate native platform/architecture proof gates must remain closed until the requirements below are met and independently reviewed. No private key or release feed is provided by this implementation.

The packaged updater is a pinned, build-time patched CommonJS bundle with its third-party notices embedded as readable comments. The patch is checked against the pinned upstream source and a syntactic module-load audit; the audit is not a proof of all possible JavaScript behavior. Release provenance depends on the isolated lockfile-verified dependency installation. The packaged updater is not imported by the production app while the gates are closed.

## Enablement requirements

- Protect the exact effective-UID-derived cache root in every agent, Coresearch and service sandbox, including forged HOME/XDG and absent-root cases. Test direct helper invocation in real sandboxes without regressing static Linux LDAP/NSS users while updates are disabled. Obtain the trusted cache and lock before network access.
- Prove APFS physical reservation and durability under copy-on-write, compression, snapshots, crashes and concurrent writers, including emergency reserve and recovery. Linux filesystem, ACL, locking and storage behavior require real-platform validation. Windows remains separately gated.
- Prove the packaged helper and its ancestors cannot be replaced between verification and execution. Test real Electron IPC, navigation, logout, quit and child reaping. Keep platform and architecture gates independent.
- Provision the authenticated release feed and public-key trust, including key rotation and a secure signing workflow. The seven-day metadata lifetime requires periodic signed refresh with serialized monotonic sequence numbers and a defined release/tag/asset mapping.
- Extend and validate safe, actionable, allowlisted error categories and user guidance without exposing raw exceptions, local paths, URLs, presigned queries or other secrets. Ambiguous failures currently remain generic; `corrupt_state` is reserved and artifact transfer errors are not yet classified. Verify integrity and authentication failures cannot be misleadingly categorized, and exercise the UI and IPC in a real Electron process.
- Design and implement native signature verification and safe installation with a cross-user, server and daemon drain protocol covering in-flight work and crash recovery. Cached artifacts are not currently installable through this feature.

The high-water protocol cannot detect complete deletion or restoration of both its state and sentinel without independent durable state. Cache accounting and reservations are per user and do not constitute a host-wide disk quota.
