// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from "react";
import { useDocumentTitle } from "../../components/Page";

/** The frame around the sign-in and setup pages. */
export function AuthLayout({ title, lede, children }: { title: string; lede?: ReactNode; children: ReactNode }) {
  useDocumentTitle(title);
  return (
    <div className="auth">
      <main className="auth-card" aria-labelledby="auth-title">
        <div className="brand auth-brand">
          <span className="brand-mark" aria-hidden="true" />
          <span className="brand-name">Signalbox</span>
        </div>
        <h1 id="auth-title">{title}</h1>
        {lede ? <div className="lede">{lede}</div> : null}
        {children}
      </main>
    </div>
  );
}
