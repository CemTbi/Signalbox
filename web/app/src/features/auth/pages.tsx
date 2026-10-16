// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, useRouter } from "@tanstack/react-router";
import { api, unwrap } from "../../api";
import { storeSession } from "../../session";
import { AuthLayout } from "./AuthLayout";
import { SetupForm, type SetupValues } from "./SetupForm";
import { SignInForm, type SignInValues } from "./SignInForm";

const signInRoute = getRouteApi("/sign-in");

export function SignInPage() {
  const { redirect } = signInRoute.useSearch();
  const router = useRouter();
  const queryClient = useQueryClient();
  const signIn = useMutation({
    mutationFn: (values: SignInValues) => unwrap(api.POST("/api/v1/sessions", { body: values })),
    onSuccess: async (session) => {
      storeSession(queryClient, session);
      await router.invalidate();
      router.history.push(redirect ?? "/");
    },
  });

  return (
    <AuthLayout title="Sign in">
      <SignInForm onSubmit={(v) => signIn.mutate(v)} pending={signIn.isPending} error={signIn.error} />
    </AuthLayout>
  );
}

export function SetupPage() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const setup = useMutation({
    mutationFn: (values: SetupValues) => unwrap(api.POST("/api/v1/setup", { body: values })),
    onSuccess: async (session) => {
      storeSession(queryClient, session);
      await router.invalidate();
      router.history.push("/workspaces");
    },
  });

  return (
    <AuthLayout
      title="Set up Signalbox"
      lede={<p>This installation has no accounts yet. Create the first one; it becomes the administrator.</p>}
    >
      <SetupForm onSubmit={(v) => setup.mutate(v)} pending={setup.isPending} error={setup.error} />
    </AuthLayout>
  );
}
