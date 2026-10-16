// SPDX-License-Identifier: Apache-2.0

import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";

/**
 * Renders a component inside a minimal router, for components that use
 * Link. The router renders asynchronously, so find elements with findBy*.
 */
export function renderWithRouter(ui: ReactNode) {
  const rootRoute = createRootRoute({ component: () => <>{ui}</> });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  return render(<RouterProvider router={router as never} />);
}
