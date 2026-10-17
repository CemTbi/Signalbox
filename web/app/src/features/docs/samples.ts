// SPDX-License-Identifier: Apache-2.0

import type { ReferenceOperation, ReferenceParameter, ReferenceSchema, ReferenceSecurityScheme } from "@signalbox/api-client";

/** A request to an operation, ready to send or to write as code. */
export interface RequestDraft {
  method: string;
  url: string;
  headers: [string, string][];
  body?: { type: string; text: string };
}

/** Values for an operation's parameters, by location and name. */
export interface ParameterValues {
  path: Record<string, string>;
  query: Record<string, string>;
  header: Record<string, string>;
}

/** A value to show for a parameter: its example, default or first allowed value, or a placeholder for its type. */
export function sampleValue(p: ReferenceParameter): string {
  const s = p.schema;
  const candidates = [p.example, s?.example, s?.default, s?.enum?.[0], s?.const];
  for (const c of candidates) {
    if (c !== undefined && c !== null) return typeof c === "string" ? c : JSON.stringify(c);
  }
  return placeholder(s);
}

function placeholder(s: ReferenceSchema | undefined): string {
  const type = s?.types?.[0];
  switch (type) {
    case "integer":
    case "number":
      return "1";
    case "boolean":
      return "true";
  }
  switch (s?.format) {
    case "date-time":
      return "2026-01-01T00:00:00Z";
    case "date":
      return "2026-01-01";
    case "uuid":
      return "00000000-0000-0000-0000-000000000000";
    case "email":
      return "name@example.com";
  }
  return "string";
}

/** The values samples use: required parameters only, each with its sample value. */
export function sampleValues(op: ReferenceOperation): ParameterValues {
  const values: ParameterValues = { path: {}, query: {}, header: {} };
  for (const p of op.parameters ?? []) {
    if (p.in !== "path" && p.in !== "query" && p.in !== "header") continue;
    if (p.in === "path" || p.required) values[p.in][p.name] = sampleValue(p);
  }
  return values;
}

/** The credentials an operation's first way to authorize needs, as headers and query parameters with placeholders. */
export function authorization(op: ReferenceOperation, schemes: ReferenceSecurityScheme[]) {
  const headers: [string, string][] = [];
  const query: [string, string][] = [];
  for (const use of op.security?.[0] ?? []) {
    const s = schemes.find((x) => x.name === use.scheme);
    if (!s) continue;
    if (s.type === "http" && s.scheme?.toLowerCase() === "basic") headers.push(["Authorization", "Basic $CREDENTIALS"]);
    else if (s.type === "http") headers.push(["Authorization", `${capitalize(s.scheme ?? "bearer")} $TOKEN`]);
    else if (s.type === "apiKey" && s.in === "query") query.push([s.parameterName ?? "api_key", "$API_KEY"]);
    else if (s.type === "apiKey" && s.in === "cookie") headers.push(["Cookie", `${s.parameterName ?? "session"}=$API_KEY`]);
    else if (s.type === "apiKey") headers.push([s.parameterName ?? "X-API-Key", "$API_KEY"]);
    else headers.push(["Authorization", "Bearer $TOKEN"]);
  }
  return { headers, query };
}

function capitalize(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1).toLowerCase();
}

/** Joins a base URL and an operation's path, with path parameters filled in and query parameters added. */
export function requestURL(base: string, path: string, values: ParameterValues, extraQuery: [string, string][] = []) {
  const filled = path.replace(/\{([^}]+)\}/g, (_, name: string) => encodeURIComponent(values.path[name] ?? `{${name}}`));
  const query = new URLSearchParams();
  for (const [k, v] of Object.entries(values.query)) if (v !== "") query.append(k, v);
  for (const [k, v] of extraQuery) query.append(k, v);
  const qs = query.toString();
  return base.replace(/\/+$/, "") + filled + (qs ? `?${qs}` : "");
}

/** The request samples show: required parameters, the first example body, and placeholder credentials. */
export function sampleRequest(op: ReferenceOperation, base: string, schemes: ReferenceSecurityScheme[]): RequestDraft {
  const values = sampleValues(op);
  const auth = authorization(op, schemes);
  const headers: [string, string][] = [...Object.entries(values.header), ...auth.headers];
  const media = op.requestBody?.content[0];
  const draft: RequestDraft = { method: op.method, url: requestURL(base, op.path, values, auth.query), headers };
  if (media) {
    draft.body = { type: media.type, text: media.examples?.[0]?.value ?? "" };
    headers.unshift(["Content-Type", media.type]);
  }
  return draft;
}

export const languages = [
  { id: "curl", label: "cURL" },
  { id: "javascript", label: "JavaScript" },
  { id: "python", label: "Python" },
  { id: "go", label: "Go" },
  { id: "java", label: "Java" },
  { id: "csharp", label: "C#" },
] as const;

export type Language = (typeof languages)[number]["id"];

/** A string literal that JavaScript, Python, Go, Java and C# all read the same way. */
function quote(s: string) {
  return JSON.stringify(s);
}

function shellQuote(s: string) {
  return `'${s.replaceAll("'", `'"'"'`)}'`;
}

/** Writes a request as code in a language. */
export function sample(lang: Language, r: RequestDraft): string {
  switch (lang) {
    case "curl":
      return curl(r);
    case "javascript":
      return javascript(r);
    case "python":
      return python(r);
    case "go":
      return goSample(r);
    case "java":
      return java(r);
    case "csharp":
      return csharp(r);
  }
}

function curl(r: RequestDraft) {
  const parts = [`curl ${r.method === "GET" ? "" : `-X ${r.method} `}${shellQuote(r.url)}`];
  for (const [k, v] of r.headers) parts.push(`-H ${shellQuote(`${k}: ${v}`)}`);
  if (r.body) parts.push(`--data ${shellQuote(r.body.text)}`);
  return parts.join(" \\\n  ");
}

function javascript(r: RequestDraft) {
  const lines = [`const response = await fetch(${quote(r.url)}, {`, `  method: ${quote(r.method)},`];
  if (r.headers.length > 0) {
    lines.push("  headers: {");
    for (const [k, v] of r.headers) lines.push(`    ${quote(k)}: ${quote(v)},`);
    lines.push("  },");
  }
  if (r.body) lines.push(`  body: ${quote(r.body.text)},`);
  lines.push("});", "console.log(response.status, await response.text());");
  return lines.join("\n");
}

function python(r: RequestDraft) {
  const lines = ["import requests", "", "response = requests.request(", `    ${quote(r.method)},`, `    ${quote(r.url)},`];
  if (r.headers.length > 0) {
    lines.push("    headers={");
    for (const [k, v] of r.headers) lines.push(`        ${quote(k)}: ${quote(v)},`);
    lines.push("    },");
  }
  if (r.body) lines.push(`    data=${quote(r.body.text)},`);
  lines.push(")", "print(response.status_code, response.text)");
  return lines.join("\n");
}

function goSample(r: RequestDraft) {
  const body = r.body ? `strings.NewReader(${quote(r.body.text)})` : "nil";
  const lines = [
    "req, err := http.NewRequest(" + quote(r.method) + ", " + quote(r.url) + ", " + body + ")",
    "if err != nil {",
    "\tlog.Fatal(err)",
    "}",
  ];
  for (const [k, v] of r.headers) lines.push(`req.Header.Set(${quote(k)}, ${quote(v)})`);
  lines.push(
    "resp, err := http.DefaultClient.Do(req)",
    "if err != nil {",
    "\tlog.Fatal(err)",
    "}",
    "defer resp.Body.Close()",
    "body, _ := io.ReadAll(resp.Body)",
    "fmt.Println(resp.Status, string(body))",
  );
  return lines.join("\n");
}

function java(r: RequestDraft) {
  const publisher = r.body ? `HttpRequest.BodyPublishers.ofString(${quote(r.body.text)})` : "HttpRequest.BodyPublishers.noBody()";
  const lines = ["HttpRequest request = HttpRequest.newBuilder()", `    .uri(URI.create(${quote(r.url)}))`];
  for (const [k, v] of r.headers) lines.push(`    .header(${quote(k)}, ${quote(v)})`);
  lines.push(
    `    .method(${quote(r.method)}, ${publisher})`,
    "    .build();",
    "HttpResponse<String> response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString());",
    "System.out.println(response.statusCode() + \" \" + response.body());",
  );
  return lines.join("\n");
}

function csharp(r: RequestDraft) {
  const lines = [
    "using var client = new HttpClient();",
    `var request = new HttpRequestMessage(new HttpMethod(${quote(r.method)}), ${quote(r.url)});`,
  ];
  for (const [k, v] of r.headers) {
    // Content headers belong to the content.
    if (k.toLowerCase() !== "content-type") lines.push(`request.Headers.TryAddWithoutValidation(${quote(k)}, ${quote(v)});`);
  }
  if (r.body) lines.push(`request.Content = new StringContent(${quote(r.body.text)}, null, ${quote(r.body.type)});`);
  lines.push(
    "var response = await client.SendAsync(request);",
    "Console.WriteLine($\"{(int)response.StatusCode} {await response.Content.ReadAsStringAsync()}\");",
  );
  return lines.join("\n");
}
