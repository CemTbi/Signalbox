/** fetch, or anything that works like it. */
export type Fetch = (input: string, init: RequestInit) => Promise<Response>;

export interface ClientOptions {
  /** The API's base URL, such as https://api.example.com/v1, or a Signalbox mock's address. */
  baseUrl: string;
  /** Headers for every request, such as credentials. A function is called before each request. */
  headers?: HeadersInit | (() => HeadersInit | Promise<HeadersInit>);
  /** The fetch to send requests with. The global fetch by default. */
  fetch?: Fetch;
}

/** Options for one request. */
export interface RequestOptions {
  /** Headers for this request, over the client's. */
  headers?: HeadersInit;
  /** Aborts the request. */
  signal?: AbortSignal;
}

/**
 * A response with a status other than 2xx. body is its body: parsed when it
 * is JSON, such as an RFC 9457 problem, or else its text.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;
  readonly response: Response;

  constructor(response: Response, body: unknown) {
    super(`${response.status} ${response.statusText || reasons[response.status] || ""}`.trim() + problemDetail(body));
    this.name = "ApiError";
    this.status = response.status;
    this.body = body;
    this.response = response;
  }
}

// HTTP/2 responses have no status text.
const reasons: Record<number, string> = {
  400: "Bad Request",
  401: "Unauthorized",
  403: "Forbidden",
  404: "Not Found",
  405: "Method Not Allowed",
  409: "Conflict",
  410: "Gone",
  412: "Precondition Failed",
  413: "Content Too Large",
  415: "Unsupported Media Type",
  422: "Unprocessable Content",
  428: "Precondition Required",
  429: "Too Many Requests",
  500: "Internal Server Error",
  501: "Not Implemented",
  502: "Bad Gateway",
  503: "Service Unavailable",
  504: "Gateway Timeout",
};

function problemDetail(body: unknown): string {
  if (typeof body === "object" && body !== null) {
    const { detail, title } = body as { detail?: unknown; title?: unknown };
    if (typeof detail === "string") return `: ${detail}`;
    if (typeof title === "string") return `: ${title}`;
  }
  return "";
}

/** How the client calls one operation. */
interface Call {
  path?: Record<string, unknown>;
  /** Name, value, and whether a list is repeated (form) or joined with commas (csv). */
  query?: [string, unknown, "form" | "csv"][];
  headers?: [string, unknown][];
  body?: unknown;
  bodyType?: string;
  bodyKind?: "json" | "text" | "form" | "multipart" | "binary";
  accept: "json" | "text" | "binary" | "form" | "multipart";
}

function text(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "object" && value !== null) return JSON.stringify(value);
  return String(value);
}

function caller(options: ClientOptions) {
  const send: Fetch = options.fetch ?? ((input, init) => fetch(input, init));
  const base = options.baseUrl.replace(/\/+$/, "");

  return async function call<T>(method: string, template: string, c: Call, request?: RequestOptions): Promise<T> {
    const path = template.replace(/\{([^}]+)\}/g, (_, name: string) => encodeURIComponent(text(c.path?.[name])));
    const query = new URLSearchParams();
    for (const [name, value, style] of c.query ?? []) {
      if (value === undefined || value === null) continue;
      if (Array.isArray(value)) {
        if (style === "csv") query.append(name, value.map(text).join(","));
        else for (const v of value) query.append(name, text(v));
      } else {
        query.append(name, text(value));
      }
    }
    const search = query.toString();

    const headers = new Headers(typeof options.headers === "function" ? await options.headers() : options.headers);
    for (const [name, value] of c.headers ?? []) {
      if (value !== undefined && value !== null) headers.set(name, text(value));
    }
    let body: BodyInit | undefined;
    if (c.body !== undefined) {
      switch (c.bodyKind) {
        case "json":
          body = JSON.stringify(c.body);
          headers.set("Content-Type", c.bodyType ?? "application/json");
          break;
        case "form":
          // fetch sets the content type of URLSearchParams and FormData.
          body = c.body instanceof URLSearchParams ? c.body : new URLSearchParams(c.body as Record<string, string>);
          break;
        case "multipart":
          body = c.body as FormData;
          break;
        default:
          body = c.body as BodyInit;
          if (c.bodyType) headers.set("Content-Type", c.bodyType);
      }
    }
    if (!headers.has("Accept")) headers.set("Accept", c.accept === "json" ? "application/json" : "*/*");
    new Headers(request?.headers).forEach((value, name) => headers.set(name, value));

    const response = await send(base + path + (search ? `?${search}` : ""), { method, headers, body, signal: request?.signal });
    if (!response.ok) throw new ApiError(response, await read(response, "auto"));
    if (response.status === 204 || response.status === 205 || method === "HEAD") return undefined as T;
    return (await read(response, c.accept)) as T;
  };
}

async function read(response: Response, accept: Call["accept"] | "auto"): Promise<unknown> {
  const type = response.headers.get("Content-Type") ?? "";
  if (accept === "json" || /[/+]json\b/i.test(type)) {
    const raw = await response.text();
    if (raw === "") return undefined;
    try {
      return JSON.parse(raw);
    } catch {
      return raw;
    }
  }
  if (accept === "binary") return response.blob();
  return response.text();
}
