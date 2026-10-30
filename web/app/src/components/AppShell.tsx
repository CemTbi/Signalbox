// SPDX-License-Identifier: Apache-2.0

import type { Session } from "@signalbox/api-client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useRouter } from "@tanstack/react-router";
import { api, unwrap } from "../api";
import { forgetSession } from "../session";
import { HealthIndicator } from "./HealthIndicator";

/** The frame of every signed-in page: the top bar and the page content. */
export function AppShell({ session }: { session: Session }) {
  return (
    <div className="shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <div className="topbar-start">
          <Link to="/workspaces" className="brand" aria-label="Signalbox home">
            <span className="brand-mark" aria-hidden="true" />
            <span className="brand-name">Signalbox</span>
            <span className="tag">pre-alpha</span>
          </Link>
          <nav aria-label="Main" className="mainnav">
            <Link to="/workspaces" activeProps={{ "aria-current": "page" }} activeOptions={{ exact: false }}>
              Workspaces
            </Link>
            <Link to="/spec-check" activeProps={{ "aria-current": "page" }}>
              Spec check
            </Link>
          </nav>
        </div>
        <div className="topbar-end">
          <HealthIndicator />
          <UserMenu session={session} />
        </div>
      </header>
      <main id="main" className="page">
        <Outlet />
      </main>
    </div>
  );
}

function UserMenu({ session }: { session: Session }) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const signOut = useMutation({
    mutationFn: () => unwrap(api.DELETE("/api/v1/sessions/current")),
    // Even if the server can't be reached, forget the session locally.
    onSettled: async () => {
      forgetSession(queryClient);
      await router.invalidate();
      router.history.push("/sign-in");
    },
  });

  return (
    <div className="user">
      <span className="user-name" title={session.user.email}>
        {session.user.name}
      </span>
      <Link to="/account/agents" className="button ghost" activeProps={{ "aria-current": "page" }}>
        Agents
      </Link>
      <Link to="/account/tokens" className="button ghost" activeProps={{ "aria-current": "page" }}>
        Access tokens
      </Link>
      <button
        type="button"
        className="button ghost"
        onClick={() => signOut.mutate()}
        disabled={signOut.isPending}
      >
        {signOut.isPending ? "Signing out…" : "Sign out"}
      </button>
    </div>
  );
}
