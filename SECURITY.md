# Security policy

## Reporting a vulnerability

Please report security vulnerabilities privately using GitHub's
[private vulnerability reporting](https://docs.github.com/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
for this repository, or by emailing **security@groundcover.com**.

Do **not** open a public issue for security reports.

We aim to acknowledge reports within a few business days and will keep you
updated on remediation progress.

## Supported versions

The SDK follows the runtime's official support window (the two most recent Go
majors). Security fixes target the latest released minor version. See the
compatibility table in `README.md`.

## Handling of telemetry data

- The SDK never sends raw client IP addresses; geo/IP is derived server-side.
- `BeforeSend` is the single chokepoint for scrubbing PII/secrets before data
  leaves the process (it sees the finalized `*Event`; return `nil` to drop).
- An optional keyed-HMAC `IdentityHasher` pseudonymizes `user.id` / `user.email`
  at the SDK boundary.

### PII surface

The SDK does **not** hard-block PII by default. Know exactly what can leave:

| Field | Default handling |
| ----- | ---------------- |
| `user.id`, `user.email` | Pseudonymized if `Hasher` is set; otherwise sent as-is |
| `user.name`, `user.organization` | Sent as-is — **not** covered by `Hasher` |
| custom `Attributes`, `error_message`, `error_stacktrace` | Sent as-is unless scrubbed in `BeforeSend` |
| client IP | Never sent (derived server-side) |
| SDK-internal logs | Record the **type** of a recovered internal panic, not its value |

If your errors or attributes may carry sensitive data, configure a `BeforeSend`
scrubber. The optional `Debug` mode prints events *after* scrubbing/hashing, so it
respects both `BeforeSend` and `Hasher`.
