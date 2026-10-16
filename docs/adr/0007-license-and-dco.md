# 0007. Apache-2.0 license and DCO sign-off

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

Signalbox is open source and won't be sold. We want companies and individuals to adopt it, run it and contribute without legal friction. Contributors need confidence about patents, and the project needs a clear record that contributions may be distributed.

## Decision

- **Code:** Apache License 2.0. Every source file starts with `SPDX-License-Identifier: Apache-2.0` in a comment. Copyright belongs to "The Signalbox Authors", meaning each contributor; there are no per-file copyright lines.
- **Documentation:** Creative Commons Attribution 4.0 (CC BY 4.0).
- **Contributions:** the Developer Certificate of Origin. Every commit carries a `Signed-off-by` line (`git commit -s`), and the DCO GitHub App checks pull requests. There is no contributor license agreement.

## Consequences

- Apache-2.0 includes an explicit patent grant and is the license of OpenTelemetry and Kubernetes, which most legal departments have already approved.
- Anyone, including a company, may offer Signalbox as a hosted service without sharing changes. We accept that, because adoption matters more to this project than control.
- Contributors keep their copyright. Changing the license later would need every contributor's agreement, which is a deliberate safeguard for users.
- The DCO costs contributors one flag. A CLA would cost them a legal review.

## Alternatives considered

- **MIT.** Simpler, but no explicit patent grant.
- **AGPL-3.0.** It would force hosted forks to publish their changes, but many companies forbid AGPL software, which would cut adoption. That trade only makes sense for a project that sells a hosted service.
- **Business Source License or similar.** Rejected. Not open source.
