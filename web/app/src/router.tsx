// SPDX-License-Identifier: Apache-2.0

import type { ApiClient } from "@signalbox/api-client";
import type { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  type ErrorComponentProps,
  lazyRouteComponent,
  Link,
  Outlet,
  redirect,
  useRouteContext,
  useRouter,
} from "@tanstack/react-router";
import { describeError } from "./api";
import { AppShell } from "./components/AppShell";
import { LoadError, PageHeader } from "./components/Page";
import { TokensPage } from "./features/account/TokensPage";
import { SetupPage, SignInPage } from "./features/auth/pages";
import { ssoFailureMessages } from "./features/auth/sso";
import { InvitePage } from "./features/invitations/InvitePage";
import { WorkspaceMembers } from "./features/members/MembersPage";
import { ProjectPage } from "./features/projects/ProjectPage";
import { RequestsPage } from "./features/requests/RequestsPage";
import { ServicePage } from "./features/services/ServicePage";
import { SpecPage } from "./features/services/SpecPage";
import { SpecCheck } from "./features/spec-check/SpecCheck";
import { WorkspaceLayout, WorkspaceProjects, WorkspacesPage } from "./features/workspaces/pages";
import { safeRedirect, sessionQuery, setupStatusQuery } from "./session";

export interface RouterContext {
  queryClient: QueryClient;
  api: ApiClient;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: Outlet,
  notFoundComponent: NotFound,
  errorComponent: LoadFailed,
});

// Pages for people who aren't signed in. Each sends people where they
// belong: setup on a new installation, the app when already signed in.

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/setup",
  beforeLoad: async ({ context }) => {
    const status = await context.queryClient.ensureQueryData(setupStatusQuery(context.api));
    if (!status.required) throw redirect({ to: "/sign-in" });
  },
  component: SetupPage,
});

const signInRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/sign-in",
  validateSearch: (search: Record<string, unknown>): { redirect?: string; sso?: string } => {
    const target = safeRedirect(search.redirect);
    // Only reasons the page knows how to explain.
    const sso = typeof search.sso === "string" && search.sso in ssoFailureMessages ? search.sso : undefined;
    return { ...(target ? { redirect: target } : {}), ...(sso ? { sso } : {}) };
  },
  beforeLoad: async ({ context }) => {
    const status = await context.queryClient.ensureQueryData(setupStatusQuery(context.api));
    if (status.required) throw redirect({ to: "/setup" });
    const session = await context.queryClient.ensureQueryData(sessionQuery(context.api));
    if (session) throw redirect({ to: "/workspaces" });
  },
  component: SignInPage,
});

// Invitation links work whether or not someone is signed in.
const inviteRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/invite",
  component: InvitePage,
});

// Everything else needs a session.

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "_app",
  beforeLoad: async ({ context, location }) => {
    const session = await context.queryClient.ensureQueryData(sessionQuery(context.api));
    if (!session) {
      const status = await context.queryClient.ensureQueryData(setupStatusQuery(context.api));
      if (status.required) throw redirect({ to: "/setup" });
      throw redirect({ to: "/sign-in", search: { redirect: location.href } });
    }
    return { session };
  },
  component: function AppLayout() {
    const { session } = useRouteContext({ from: "/_app" });
    return <AppShell session={session} />;
  },
});

const homeRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/workspaces" });
  },
});

const workspacesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/workspaces",
  component: WorkspacesPage,
});

const workspaceRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/workspaces/$workspaceId",
  component: WorkspaceLayout,
});

const workspaceProjectsRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: "/",
  component: WorkspaceProjects,
});

const workspaceMembersRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: "/members",
  component: WorkspaceMembers,
});

const projectRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/projects/$projectId",
  component: ProjectPage,
});

const requestsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/projects/$projectId/requests",
  component: RequestsPage,
});

const serviceRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/services/$serviceId",
  component: ServicePage,
});

const specRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/specs/$specId",
  component: SpecPage,
});

const docsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/specs/$specId/docs",
  validateSearch: (search: Record<string, unknown>): { version?: string } =>
    typeof search.version === "string" && search.version !== "" ? { version: search.version } : {},
  // Docs bring a Markdown renderer that other pages don't need.
  component: lazyRouteComponent(() => import("./features/docs/DocsPage"), "DocsPage"),
});

const tokensRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/account/tokens",
  component: TokensPage,
});

const specCheckRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/spec-check",
  component: function SpecCheckPage() {
    return (
      <div className="stack">
        <PageHeader title="Spec check" documentTitle="Spec check" />
        <SpecCheck />
      </div>
    );
  },
});

const routeTree = rootRoute.addChildren([
  setupRoute,
  signInRoute,
  inviteRoute,
  appRoute.addChildren([
    homeRoute,
    workspacesRoute,
    workspaceRoute.addChildren([workspaceProjectsRoute, workspaceMembersRoute]),
    projectRoute,
    requestsRoute,
    serviceRoute,
    specRoute,
    docsRoute,
    specCheckRoute,
    tokensRoute,
  ]),
]);

export function createAppRouter(context: RouterContext) {
  return createRouter({
    routeTree,
    context,
    defaultPreload: "intent",
    // Data loads through TanStack Query, which has its own cache.
    defaultPreloadStaleTime: 0,
    scrollRestoration: true,
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}

/**
 * Shown when a page can't load at all, for example because the server
 * can't be reached while the app checks the session.
 */
function LoadFailed({ error, reset }: ErrorComponentProps) {
  const router = useRouter();
  return (
    <main className="page narrow">
      <PageHeader title="This page couldn't load" />
      <LoadError
        message={describeError(error)}
        onRetry={() => {
          reset();
          void router.invalidate();
        }}
      />
    </main>
  );
}

function NotFound() {
  return (
    <main className="page narrow">
      <PageHeader title="Page not found" />
      <p>There is nothing at this address.</p>
      <p>
        <Link to="/workspaces">Go to your workspaces</Link>
      </p>
    </main>
  );
}
