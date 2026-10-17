// SPDX-License-Identifier: Apache-2.0

// Package telemetry stores and reads what Signalbox observes about running
// services: spans from OpenTelemetry and Zipkin, the traces they form, and
// each operation's calls, errors and latency per minute. It keeps them in
// ClickHouse. See docs/adr/0014-span-storage.md.
package telemetry
