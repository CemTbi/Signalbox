# 0016. Where the runner may send requests

- Status: Accepted
- Date: 2026-10-17
- Deciders: project lead

## Context

The API client, tests, monitors and the docs' Try it console send requests to addresses that users type, from the Signalbox server. Without a check, anyone who can send a request can make the server reach places only it can reach: a cloud metadata endpoint that hands out the machine's credentials, Signalbox's own PostgreSQL and ClickHouse, or its ops listener on loopback. This is server-side request forgery, and [SECURITY.md](../../SECURITY.md) names it, with DNS rebinding and metadata endpoints, as a focus area.

A check on the URL alone isn't enough:

- A hostname can resolve to an internal address, and to a different one the second time it is resolved (DNS rebinding), so checking a name and then connecting to it checks nothing.
- An internal IPv4 address can hide inside an IPv6 address: `::ffff:169.254.169.254`, NAT64 `64:ff9b::a9fe:a9fe`, 6to4 and Teredo.
- A public URL can redirect to an internal one.
- A proxy from `HTTP_PROXY` would receive the request, so the proxy's address would be checked instead of the destination's.

At the same time, calling services on private networks is the runner's job: staging APIs on `10.x` addresses are what teams test. Blocking every private range by default would make the runner useless on most installations.

Spike 5 had to show that one dialer closes these gaps without blocking legitimate use, and what it costs.

## Decision

Every outbound request on a user's behalf goes through `internal/platform/egress`: a `Dialer` with a `Policy`, and an HTTP client built on it.

**Resolve once, check every answer, dial only what passed.** The dialer resolves the hostname itself, drops every address the policy refuses, and connects to the remaining addresses directly. Just before each socket connects, its address is checked again, so nothing reaches the network unchecked. Redirects dial again and are checked the same way.

**The policy, first match wins:**

1. Signalbox's own services (`Protected`, which the runner role will fill from the configured database addresses): always refused.
2. Ranges and hostnames an admin denies: refused.
3. Ranges and hostnames an admin allows: allowed. An allowed hostname skips the built-in blocks, whatever it resolves to.
4. Built-in blocks: loopback, unspecified, link-local (which holds the AWS, Azure, Google and Oracle metadata endpoints at `169.254.169.254`), the Alibaba and Oracle metadata endpoints elsewhere, AWS's IPv6 endpoint `fd00:ec2::254`, the local-use NAT64 prefix `64:ff9b:1::/48`, multicast, broadcast and reserved addresses, and addresses with an IPv6 zone.
5. Everything else, private networks included: allowed.

An IPv6 address that carries an IPv4 address must pass for both.

**The HTTP client** ignores proxy environment variables, follows at most 10 redirects, gives up after 30 seconds and reads at most 10 MB of a response by default. Each use can set its own limits.

## Spike results (2026-10-17)

| Question | Result |
|---|---|
| Built-in blocks | Loopback, `0.0.0.0`, metadata endpoints on all five clouds, multicast, broadcast and link-local addresses are refused, on IPv4 and IPv6. Private ranges and public addresses pass. |
| Hidden addresses | `::ffff:`, IPv4-compatible, NAT64 (both prefixes), 6to4 and Teredo forms of loopback and metadata addresses are refused; a NAT64 form of a public address passes. With the embedded check turned off, five test cases fail. |
| DNS rebinding | A name that answers with the metadata endpoint and an allowed address is resolved once per connection, and only the allowed address is dialed. A name that answers only inside is refused before any packet is sent. |
| Redirects and proxies | A redirect to `169.254.169.254` is refused. With `HTTP_PROXY` set, requests still go direct. |
| Limits | Bodies over the limit fail, both declared by `Content-Length` and streamed; redirect loops stop. |
| Cost | The policy check takes 250 ns per connection with no allocations, on an AMD Ryzen 5 3600 with a typical admin policy. A TCP handshake on a LAN takes about 1,000 times longer. |

## Consequences

- One package carries the whole policy, so the API client, tests, monitors and Try it can't disagree about it, and a security review has one place to look.
- Admins of installations without other protection can deny their private ranges and allow only the subnets they test.
- `Protected` holds addresses, resolved when the server starts. If a database moves to another address while Signalbox runs, the old address stays protected and the new one isn't until a restart. The runner role must re-resolve them periodically when it is built.
- Only TCP is covered. HTTP/3 over UDP, gRPC and WebSocket clients must use this dialer too when they arrive.
- The local-use NAT64 prefix `64:ff9b:1::/48` (RFC 8215) is refused outright. A site's translator may put the IPv4 address in any of four places inside it, and the dialer can't know which, so an address that looks harmless in one layout could reach a metadata endpoint in another. If an admin allows the prefix, an address must still pass in all four layouts. A review of the first version found that it read only one layout.
- Addresses are tried one after another, not in parallel as Happy Eyeballs does, so a host whose first address doesn't answer is reached only after the connect timeout.
- `signalbox agent` runs on a person's own machine to reach localhost and their private networks. It needs a different default policy, decided when the agent is built.
- Kubernetes installations should still isolate runner pods with a NetworkPolicy, as the build plan says. The dialer is the first line, not the only one.

## Alternatives considered

- **Check the URL's host before sending.** Rejected. It misses DNS rebinding, hidden IPv4 addresses and redirects.
- **Block every private range by default.** Rejected. It breaks the runner's main use, testing internal services, and admins would turn the protection off entirely.
- **An egress proxy such as Smokescreen in front of the runner.** Rejected for now. It is another service to run on every installation, and a Go dialer with the same rules covers single machines. Installations that already run one can still send traffic through it.
