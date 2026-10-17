// SPDX-License-Identifier: Apache-2.0

import type { InvitationDetails, Session } from "@signalbox/api-client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { type ReactNode, useState } from "react";
import { ApiError, api, describeError, unwrap } from "../../api";
import { forgetSession, sessionQuery, storeSession } from "../../session";
import { AuthLayout } from "../auth/AuthLayout";
import { rememberReturnHash, signInOptionsQuery, ssoStartURL, takeStashedHash } from "../auth/sso";
import { roleLabels } from "../workspaces/roles";
import { SignUpForm, type SignUpValues } from "./SignUpForm";

/**
 * The page an invitation link opens. The token is in the URL's fragment,
 * after "#", which the browser never sends to the server; this page reads
 * it and sends it in request bodies.
 */
export function InvitePage() {
  const hash = useRouterState({ select: (s) => s.location.hash });
  // After single sign-on, the token waited in session storage.
  const [token] = useState(() => hash.replace(/^#/, "") || takeStashedHash("/invite") || "");
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const details = useQuery({
    queryKey: ["invitation", token],
    queryFn: () => unwrap(api.POST("/api/v1/invitations/lookup", { body: { token } })),
    enabled: token !== "",
    retry: false,
  });
  const session = useQuery(sessionQuery(api));
  const options = useQuery(signInOptionsQuery(api));

  const goToWorkspace = (workspaceId: string) => navigate({ to: "/workspaces/$workspaceId", params: { workspaceId } });
  const accept = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/invitations/accept", { body: { token } })),
    onSuccess: async (ws) => {
      await queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      await goToWorkspace(ws.id);
    },
  });
  const signUp = useMutation({
    mutationFn: (values: SignUpValues) =>
      unwrap(api.POST("/api/v1/invitations/sign-up", { body: { token, ...values } })),
    onSuccess: async (newSession) => {
      storeSession(queryClient, newSession);
      if (details.data) await goToWorkspace(details.data.workspace.id);
    },
  });
  const signOut = useMutation({
    mutationFn: () => unwrap(api.DELETE("/api/v1/sessions/current")),
    onSettled: () => forgetSession(queryClient),
  });

  if (token === "") {
    return (
      <InviteProblem title="This invitation link is incomplete">
        Copy the whole link from the message you received, including the part after <code>#</code>.
      </InviteProblem>
    );
  }
  if (details.isPending || session.isPending) {
    return (
      <AuthLayout title="Opening invitation">
        <p className="loading" role="status">
          Checking the invitation…
        </p>
      </AuthLayout>
    );
  }
  if (details.isError) {
    const gone = details.error instanceof ApiError && details.error.status === 404;
    return (
      <InviteProblem title={gone ? "This invitation can't be used" : "The invitation couldn't load"}>
        {gone
          ? "The link is wrong, has expired or was already used. Ask the person who invited you for a new one."
          : describeError(details.error)}
      </InviteProblem>
    );
  }

  return (
    <InviteView
      details={details.data}
      session={session.data ?? null}
      token={token}
      ssoName={options.data?.sso?.name}
      accept={{ run: () => accept.mutate(), pending: accept.isPending, error: accept.error }}
      signUp={{ run: (v) => signUp.mutate(v), pending: signUp.isPending, error: signUp.error }}
      signOut={{ run: () => signOut.mutate(), pending: signOut.isPending }}
    />
  );
}

interface Action<T = void> {
  run: (value: T) => void;
  pending: boolean;
  error?: unknown;
}

/** What the invitation page shows, given the invitation and who is signed in. */
export function InviteView({
  details,
  session,
  token,
  ssoName,
  accept,
  signUp,
  signOut,
}: {
  details: InvitationDetails;
  session: Session | null;
  token: string;
  // The identity provider's name, when single sign-on is set up.
  ssoName?: string;
  accept: Action;
  signUp: Action<SignUpValues>;
  signOut: Action;
}) {
  const role = roleLabels[details.role].toLowerCase();
  const offer = (
    <p>
      {details.invitedBy ? `${details.invitedBy} invited ` : "You're invited "}
      <strong>{details.email}</strong> to join <strong>{details.workspace.name}</strong> as {article(role)} {role}.
    </p>
  );
  const title = `Join ${details.workspace.name}`;

  if (session && session.user.email.toLowerCase() === details.email.toLowerCase()) {
    return (
      <AuthLayout title={title} lede={offer}>
        {accept.error ? (
          <p className="form-alert" role="alert">
            {describeError(accept.error)}
          </p>
        ) : null}
        <button type="button" className="button primary wide" disabled={accept.pending} onClick={() => accept.run()}>
          {accept.pending ? "Joining…" : `Join ${details.workspace.name}`}
        </button>
      </AuthLayout>
    );
  }

  if (session) {
    return (
      <AuthLayout title={title} lede={offer}>
        <p>
          You're signed in as <strong>{session.user.email}</strong>. Sign out, then sign in or create an account as{" "}
          {details.email} to accept.
        </p>
        <button type="button" className="button secondary wide" disabled={signOut.pending} onClick={() => signOut.run()}>
          {signOut.pending ? "Signing out…" : "Sign out"}
        </button>
      </AuthLayout>
    );
  }

  const sso = ssoName ? (
    <>
      <a
        className="button secondary wide"
        href={ssoStartURL("/invite")}
        onClick={() => rememberReturnHash(`/invite#${token}`)}
      >
        Continue with {ssoName}
      </a>
      <p className="divider" aria-hidden="true">
        <span>or</span>
      </p>
    </>
  ) : null;

  if (details.accountExists) {
    return (
      <AuthLayout title={title} lede={offer}>
        <p>Sign in as {details.email} to accept. You'll come back here afterwards.</p>
        <Link to="/sign-in" search={{ redirect: `/invite#${token}` }} className="button primary wide">
          Sign in
        </Link>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout title={title} lede={offer}>
      {sso}
      <SignUpForm
        email={details.email}
        onSubmit={(v) => signUp.run(v)}
        pending={signUp.pending}
        error={signUp.error}
      />
    </AuthLayout>
  );
}

function InviteProblem({ title, children }: { title: string; children: ReactNode }) {
  return (
    <AuthLayout title={title}>
      <p>{children}</p>
      <Link to="/sign-in">Go to sign-in</Link>
    </AuthLayout>
  );
}

function article(word: string) {
  return /^[aeiou]/.test(word) ? "an" : "a";
}
