// SPDX-License-Identifier: Apache-2.0

import type { Report } from "@signalbox/api-client";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../../api";
import { ServiceForm, titleOf } from "./forms";
import { SpecEditor } from "./SpecEditor";
import { renderWithRouter } from "../../test/router";
import { mockURL, Versions } from "./Versions";

describe("titleOf", () => {
  it("finds the title in YAML and JSON documents", () => {
    expect(titleOf("openapi: 3.1.1\ninfo:\n  version: 1.0.0\n  title: Orders API\npaths: {}\n")).toBe("Orders API");
    expect(titleOf("openapi: 3.1.1\ninfo:\n  title: 'Quoted Title'\n")).toBe("Quoted Title");
    expect(titleOf('{"openapi":"3.0.3","info":{"version":"1","title":"Payments"},"paths":{}}')).toBe("Payments");
    expect(titleOf("paths: {}\n")).toBeUndefined();
  });

  it("reads YAML's info block line by line", () => {
    expect(titleOf("info:\r\n  title: Windows Lines\r\n")).toBe("Windows Lines");
    expect(titleOf("info:\n  x-meta:\n    title: Nested\n\n  # a comment\n  title: Orders # the public name\n")).toBe("Orders");
    expect(titleOf("info:\n  version: 1.0.0\npaths:\n  title: Not Info\n")).toBeUndefined();
    expect(titleOf(`info:\n  title: ${"x".repeat(101)}\n`)).toBeUndefined();
  });

  it("reads quoted YAML titles up to their closing quote", () => {
    expect(titleOf('info:\n  title: "Orders" # the public name\n')).toBe("Orders");
    expect(titleOf("info:\n  title: 'Bob''s API'\n")).toBe("Bob's API");
    expect(titleOf('info:\n  title: "Say \\"hi\\""\n')).toBe('Say "hi"');
    expect(titleOf('info:\n  title: "Unclosed\n')).toBeUndefined();
  });

  it("returns at once on input that made the old pattern backtrack", () => {
    const started = performance.now();
    expect(titleOf("info:\n" + "\t\t\n".repeat(50_000))).toBeUndefined();
    expect(performance.now() - started).toBeLessThan(1000);
  });
});

describe("ServiceForm", () => {
  it("suggests a slug from the name until the slug is edited", async () => {
    const onSubmit = vi.fn();
    render(<ServiceForm onSubmit={onSubmit} pending={false} error={null} />);
    await userEvent.type(screen.getByLabelText("Name"), "Orders API");
    expect(screen.getByLabelText("Slug")).toHaveValue("orders-api");
    await userEvent.clear(screen.getByLabelText("Slug"));
    await userEvent.type(screen.getByLabelText("Slug"), "orders");
    await userEvent.type(screen.getByLabelText("Name"), " v2");
    expect(screen.getByLabelText("Slug")).toHaveValue("orders");
    await userEvent.click(screen.getByRole("button", { name: "Add service" }));
    expect(onSubmit).toHaveBeenCalledWith({ name: "Orders API v2", slug: "orders" });
  });
});

describe("SpecEditor", () => {
  const report: Report = {
    format: "openapi-3.1",
    version: "3.1.1",
    diagnostics: [
      {
        severity: "warning",
        source: "linter",
        code: "operation-operationId",
        message: "The GET operation has no operationId.",
        range: { start: { line: 6, column: 5 }, end: { line: 6, column: 5 } },
      },
    ],
    counts: { errors: 0, warnings: 1, infos: 0, hints: 0 },
  };
  const doc = "openapi: 3.1.1\ninfo:\n  title: Orders API\n  version: 1.0.0\npaths:\n  /orders:\n    get: {}\n";
  const props = { content: doc, report, canEdit: true, saving: false, error: null, onSave: vi.fn(), onReload: vi.fn() };

  it("saves the changed document with a note", async () => {
    const onSave = vi.fn();
    render(<SpecEditor {...props} onSave={onSave} />);
    const save = screen.getByRole("button", { name: "Save revision" });
    expect(save).toBeDisabled();
    expect(screen.getByText("1 warning", { exact: false })).toBeInTheDocument();
    expect(screen.getByText("The GET operation has no operationId.")).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText("Document (YAML or JSON)"), "# done");
    await userEvent.type(screen.getByLabelText("What changed"), "Add a comment");
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
    await userEvent.click(save);
    expect(onSave).toHaveBeenCalledWith(`${doc}# done`, "Add a comment");
  });

  it("shows the document read-only to people who can't edit", () => {
    render(<SpecEditor {...props} canEdit={false} />);
    expect(screen.getByLabelText("Document (YAML or JSON)")).toHaveAttribute("readonly");
    expect(screen.queryByRole("button", { name: "Save revision" })).toBeNull();
  });

  it("explains a conflicting save and offers the newest version", async () => {
    const onReload = vi.fn();
    const conflict = new ApiError(412, { type: "about:blank", title: "Precondition Failed", status: 412, code: "edit-conflict" });
    render(<SpecEditor {...props} error={conflict} onReload={onReload} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Someone saved a newer version");
    await userEvent.click(screen.getByRole("button", { name: "Load the newest version" }));
    expect(onReload).toHaveBeenCalled();
  });

  it("shows a newly saved revision", () => {
    const { rerender } = render(<SpecEditor {...props} />);
    rerender(<SpecEditor {...props} content={`${doc}# v2\n`} />);
    expect(screen.getByLabelText("Document (YAML or JSON)")).toHaveValue(`${doc}# v2\n`);
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  });
});

describe("Versions", () => {
  const revision = {
    id: "rev_01j9z3s7v9x1z3b5d7f9h1k3m5",
    specId: "spec_01j9z3r4t6v8x0z2b4d6f8h0k2",
    number: 3,
    format: "openapi-3.1" as const,
    version: "3.1.1",
    apiVersion: "1.4.0",
    sizeBytes: 100,
    sha256: "0".repeat(64),
    counts: { errors: 0, warnings: 2, infos: 0, hints: 0 },
    createdBy: "usr_01j9z3kq4m8r2t6v0x5w7y9a1b",
    createdAt: "2026-10-17T12:00:00Z",
  };
  const props = { versions: [], current: revision, canPublish: true, pending: false, error: null, onPublish: vi.fn() };

  it("publishes the current revision under the document's version", async () => {
    const onPublish = vi.fn();
    render(<Versions {...props} onPublish={onPublish} />);
    expect(screen.getByText("Nothing published yet")).toBeInTheDocument();
    expect(screen.getByLabelText("Version")).toHaveValue("1.4.0");
    await userEvent.type(screen.getByLabelText("Release notes (optional)"), "Adds refunds.");
    await userEvent.click(screen.getByRole("button", { name: "Publish 1.4.0" }));
    expect(onPublish).toHaveBeenCalledWith({ name: "1.4.0", notes: "Adds refunds." });
  });

  it("explains why a revision with errors can't be published", () => {
    render(<Versions {...props} current={{ ...revision, counts: { ...revision.counts, errors: 2 } }} />);
    expect(screen.getByText(/Revision 3 has 2 errors/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Publish/ })).toBeNull();
  });

  it("says when the current revision is already published", async () => {
    const version = {
      id: "ver_01j9z3v2x4z6b8d0f2h4k6m8p0",
      specId: revision.specId,
      name: "1.4.0",
      revisionId: revision.id,
      revisionNumber: 3,
      publishedBy: revision.createdBy,
      publishedAt: "2026-10-17T12:30:00Z",
    };
    renderWithRouter(<Versions {...props} versions={[version]} />);
    expect(await screen.findByText(/is published as/)).toHaveTextContent("1.4.0");
    expect(screen.getByText("1.4.0", { selector: ".row-title .mono" })).toBeInTheDocument();
  });

  it("shows the mock addresses and docs of published versions", async () => {
    const version = {
      id: "ver_01j9z3v2x4z6b8d0f2h4k6m8p0",
      specId: revision.specId,
      name: "1.4.0",
      revisionId: revision.id,
      revisionNumber: 3,
      publishedBy: revision.createdBy,
      publishedAt: "2026-10-17T12:30:00Z",
    };
    renderWithRouter(<Versions {...props} versions={[version]} />);
    expect(await screen.findByLabelText("Mock of the latest version")).toHaveValue(mockURL(revision.specId, "latest"));
    expect(screen.getByText(mockURL(revision.specId, "1.4.0"))).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Read the docs" })).toHaveAttribute("href", `/specs/${revision.specId}/docs`);
    expect(screen.getByRole("link", { name: "Docs of version 1.4.0" })).toHaveAttribute("href", `/specs/${revision.specId}/docs?version=1.4.0`);
  });

  it("builds mock addresses on this installation", () => {
    expect(mockURL("spec_1", "1.0.0+build", "https://signalbox.example.com")).toBe(
      "https://signalbox.example.com/mocks/spec_1/1.0.0%2Bbuild",
    );
  });

  it("names a taken version at the field", () => {
    const taken = new ApiError(409, { type: "about:blank", title: "Conflict", status: 409, code: "version-exists" });
    render(<Versions {...props} error={taken} />);
    expect(screen.getByText("This spec already has a version with this name.")).toBeInTheDocument();
  });

  it("offers nothing to people who can't publish", () => {
    render(<Versions {...props} canPublish={false} />);
    expect(screen.queryByRole("button", { name: /Publish/ })).toBeNull();
  });
});
