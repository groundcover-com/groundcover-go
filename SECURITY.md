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
  leaves the process.
- An optional keyed-HMAC `IdentityHasher` pseudonymizes `user.id` / `user.email`
  at the SDK boundary.
