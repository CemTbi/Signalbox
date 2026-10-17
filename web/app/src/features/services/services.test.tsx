// SPDX-License-Identifier: Apache-2.0

import type { Report } from "@signalbox/api-client";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../../api";
import { ServiceForm, titleOf } from "./forms";
import { SpecEditor } from "./SpecEditor";

describe("titleOf", () => {
  it("finds the title in YAML and JSON documents", () => {
    expect(titleOf("openapi: 3.1.1\ninfo:\n  version: 1.0.0\n  title: Orders API\npaths: {}\n")).toBe("Orders API");
    expect(titleOf("openapi: 3.1.1\ninfo:\n  title: 'Quoted Title'\n")).toBe("Quoted Title");
    expect(titleOf('{"openapi":"3.0.3","info":{"version":"1","title":"Payments"},"paths":{}}')).toBe("Payments");
    expect(titleOf("paths: {}\n")).toBeUndefined();
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
