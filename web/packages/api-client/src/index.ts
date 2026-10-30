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
export type FieldError = Schemas["FieldError"];
export type User = Schemas["User"];
export type AccessToken = Schemas["AccessToken"];
export type NewAccessToken = Schemas["NewAccessToken"];
export type Agent = Schemas["Agent"];
export type HistoryEntry = Schemas["HistoryEntry"];
export type HistorySummary = Schemas["HistorySummary"];
export type HttpHeader = Schemas["HttpHeader"];
export type SendRequest = Schemas["SendRequest"];
export type Environment = Schemas["Environment"];
export type EnvironmentVariable = Schemas["EnvironmentVariable"];
export type EnvironmentVariables = Schemas["EnvironmentVariables"];
export type Collection = Schemas["Collection"];
export type Item = Schemas["Item"];
export type SavedRequest = Schemas["SavedRequest"];
export type RequestAuth = Schemas["RequestAuth"];
export type ContractRef = Schemas["ContractRef"];
export type Run = Schemas["Run"];
export type RunStart = Schemas["RunStart"];
export type RunStep = Schemas["RunStep"];
export type RunTotals = Schemas["RunTotals"];
export type Session = Schemas["Session"];
export type SetupStatus = Schemas["SetupStatus"];
export type SignInOptions = Schemas["SignInOptions"];
export type Workspace = Schemas["Workspace"];
export type WorkspaceRole = Schemas["WorkspaceRole"];
export type WorkspacePage = Schemas["WorkspacePage"];
export type Project = Schemas["Project"];
export type ProjectPage = Schemas["ProjectPage"];
export type Member = Schemas["Member"];
export type Invitation = Schemas["Invitation"];
export type NewInvitation = Schemas["NewInvitation"];
export type InvitationDetails = Schemas["InvitationDetails"];
export type Service = Schemas["Service"];
export type Spec = Schemas["Spec"];
export type Revision = Schemas["Revision"];
export type Version = Schemas["Version"];
export type Reference = Schemas["Reference"];
export type ReferenceGroup = Schemas["ReferenceGroup"];
export type ReferenceOperation = Schemas["ReferenceOperation"];
export type ReferenceParameter = Schemas["ReferenceParameter"];
export type ReferenceMedia = Schemas["ReferenceMedia"];
export type ReferenceResponse = Schemas["ReferenceResponse"];
export type ReferenceSchema = Schemas["ReferenceSchema"];
export type ReferenceSecurityScheme = Schemas["ReferenceSecurityScheme"];

export type ApiClient = ReturnType<typeof createClient<paths>>;

/** The header that carries the session's CSRF token on unsafe requests. */
export const CSRF_HEADER = "X-CSRF-Token";

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
