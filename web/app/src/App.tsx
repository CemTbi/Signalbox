// SPDX-License-Identifier: Apache-2.0

import { HealthIndicator } from "./components/HealthIndicator";
import { SpecCheck } from "./features/spec-check/SpecCheck";

export function App() {
  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true" />
          <span className="brand-name">Signalbox</span>
          <span className="tag">pre-alpha</span>
        </div>
        <HealthIndicator />
      </header>
      <main className="page">
        <SpecCheck />
      </main>
    </div>
  );
}
