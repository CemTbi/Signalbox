// SPDX-License-Identifier: Apache-2.0

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ApiError, api, onSessionEnded } from "./api";
import { createAppRouter } from "./router";
import { forgetSession } from "./session";
import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // A 4xx won't change by asking again; anything else might.
      retry: (failures, error) => !(error instanceof ApiError && error.status < 500) && failures < 2,
      refetchOnWindowFocus: true,
    },
  },
});

const router = createAppRouter({ queryClient, api });

// When the session ends while the app is open, for example because it
// expired, go to sign-in and come back here afterwards.
onSessionEnded(() => {
  const here = router.state.location.href;
  forgetSession(queryClient);
  void router.navigate({ to: "/sign-in", search: { redirect: here } });
});

const root = document.getElementById("root");
if (!root) {
  throw new Error("index.html has no #root element");
}

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
