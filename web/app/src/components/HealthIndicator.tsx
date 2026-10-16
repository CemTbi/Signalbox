// SPDX-License-Identifier: Apache-2.0

import type { ApiClient, HealthStatus } from "@signalbox/api-client";
import { useEffect, useState } from "react";
import { api } from "../api";

const POLL_INTERVAL_MS = 15_000;

type State =
  | { kind: "checking" }
  | { kind: "known"; status: HealthStatus; version?: string }
  | { kind: "unreachable" };

const labels: Record<HealthStatus, string> = {
  pass: "Healthy",
  warn: "Degraded",
  fail: "Unavailable",
};

/** Shows the server's readiness and version, refreshed every 15 seconds. */
export function HealthIndicator({ client = api }: { client?: ApiClient }) {
  const [state, setState] = useState<State>({ kind: "checking" });

  useEffect(() => {
    let cancelled = false;
    async function refresh() {
      try {
        const [ready, meta] = await Promise.all([client.GET("/readyz"), client.GET("/api/v1/meta")]);
        const health = ready.data ?? ready.error;
        if (cancelled) return;
        setState(
          health && "status" in health
            ? { kind: "known", status: health.status, version: meta.data?.version }
            : { kind: "unreachable" },
        );
      } catch {
        if (!cancelled) setState({ kind: "unreachable" });
      }
    }
    void refresh();
    const timer = setInterval(() => void refresh(), POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [client]);

  const tone = state.kind === "known" ? state.status : state.kind === "unreachable" ? "fail" : "unknown";
  const label =
    state.kind === "known" ? labels[state.status] : state.kind === "unreachable" ? "Server unreachable" : "Checking…";

  return (
    <div className="health" role="status" aria-live="polite">
      <span className={`health-dot health-${tone}`} aria-hidden="true" />
      <span>{label}</span>
      {state.kind === "known" && state.version ? <span className="health-version">v{state.version}</span> : null}
    </div>
  );
}
