// SPDX-License-Identifier: Apache-2.0

// Typed client for Signalbox's REST API. The types in schema.d.ts are
// generated from api/openapi.yaml with `npm run generate:api`; never edit
// them by hand.
import createClient, { type ClientOptions } from "openapi-fetch";
import type { components, paths } from "./schema";

export type { components, paths };

type Schemas = components["schemas"];
export type Meta = Schemas["Meta"];
export type HealthResponse = Schemas["HealthResponse"];
export type HealthStatus = Schemas["HealthStatus"];
export type Report = Schemas["Report"];
export type Diagnostic = Schemas["Diagnostic"];
export type Problem = Schemas["Problem"];

export type ApiClient = ReturnType<typeof createClient<paths>>;

/**
 * Creates a client for the Signalbox API. In the browser it talks to the
 * server the app was loaded from, so no URL needs configuring.
 */
export function createApiClient(options: ClientOptions = {}): ApiClient {
  return createClient<paths>({
    baseUrl: globalThis.location?.origin ?? "",
    ...options,
  });
}
