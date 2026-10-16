// SPDX-License-Identifier: Apache-2.0

import { createApiClient, type Report } from "@signalbox/api-client";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { lineRange } from "./lines";
import { sampleSpec } from "./sample";
import { SpecCheck } from "./SpecCheck";

const report: Report = {
  format: "openapi-3.1",
  version: "3.1.1",
  diagnostics: [
    {
      severity: "error",
      source: "resolver",
      code: "unresolved-reference",
      message: "Component `#/components/schemas/Order` does not exist in the specification.",
      range: { start: { line: 24, column: 17 }, end: { line: 24, column: 17 } },
      howToFix: "Check that the referenced component exists.",
    },
  ],
  counts: { errors: 1, warnings: 0, infos: 0, hints: 0 },
};

function clientReturning(response: () => Response) {
  const fetch = vi.fn(async (_request: Request) => response());
  return { fetch, client: createApiClient({ baseUrl: "http://signalbox.test", fetch }) };
}

describe("SpecCheck", () => {
  it("checks the sample document when it opens", async () => {
    const { fetch, client } = clientReturning(() => Response.json(report));
    render(<SpecCheck client={client} />);

    expect(await screen.findByText("1 error, 0 warnings, 0 info, 0 hints")).toBeInTheDocument();
    expect(screen.getByText("unresolved-reference")).toBeInTheDocument();

    const request = fetch.mock.calls[0]![0];
    expect(request.method).toBe("POST");
    expect(request.url).toBe("http://signalbox.test/api/v1/spec-checks?ruleSets=recommended");
    expect(request.headers.get("Content-Type")).toBe("application/yaml");
    expect(await request.text()).toBe(sampleSpec);
  });

  it("selects the line of a problem when it is clicked", async () => {
    const { client } = clientReturning(() => Response.json(report));
    render(<SpecCheck client={client} />);

    await userEvent.click(await screen.findByRole("button", { name: /Line 24:17/ }));

    const editor = screen.getByLabelText("Document (YAML or JSON)") as HTMLTextAreaElement;
    expect(editor).toHaveFocus();
    expect(editor.selectionStart).toBe(lineRange(sampleSpec, 24).start);
    expect(editor.selectionEnd).toBe(lineRange(sampleSpec, 24).end);
  });

  it("explains a problem response from the server", async () => {
    const { client } = clientReturning(() =>
      Response.json(
        { type: "about:blank", title: "Request Entity Too Large", status: 413, detail: "The document is larger than 10 MiB." },
        { status: 413, headers: { "Content-Type": "application/problem+json" } },
      ),
    );
    render(<SpecCheck client={client} />);

    expect(await screen.findByRole("alert")).toHaveTextContent("The document is larger than 10 MiB.");
  });

  it("explains when the server can't be reached", async () => {
    const fetch = vi.fn(async () => {
      throw new TypeError("fetch failed");
    });
    render(<SpecCheck client={createApiClient({ baseUrl: "http://signalbox.test", fetch })} />);

    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't reach the Signalbox server");
  });
});
