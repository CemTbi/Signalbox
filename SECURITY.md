# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub. Open the repository's **Security** tab and choose **Report a vulnerability**. Please don't open a public issue or discuss the problem in public channels.

Include:

- the affected version or commit
- steps to reproduce, or a proof of concept
- the impact you expect, such as data exposure or code execution

We confirm receipt within 3 business days and keep you updated while we work on a fix.

## Disclosure

We follow coordinated disclosure. Please keep the details private until a fix is released or 90 days have passed, whichever comes first. We publish a GitHub Security Advisory with each fix, request a CVE where it applies, and credit reporters unless they ask us not to.

## Supported versions

| Version | Security fixes |
|---|---|
| `main` | Yes |
| Latest pre-1.0 release | Yes |
| Older pre-1.0 releases | No, please upgrade |

From 1.0 on, the two latest minor versions receive security fixes.

## Areas of particular interest

Signalbox runs user-supplied scripts, sends requests into private networks and parses untrusted documents, so these areas matter most:

- the request runner and agent: server-side request forgery, DNS rebinding, reaching cloud metadata endpoints
- the script sandbox: escaping it, or exhausting CPU or memory
- isolation between workspaces
- authentication, sessions, API tokens and single sign-on
- storage and handling of secrets
- parsing OpenAPI documents, `$ref` resolution, and trace ingestion (OTLP and Zipkin)
