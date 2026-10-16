// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../api";
import { SetupForm } from "./auth/SetupForm";
import { SignInForm } from "./auth/SignInForm";
import { ProjectForm } from "./workspaces/ProjectForm";
import { WorkspaceForm } from "./workspaces/WorkspaceForm";

function problem(status: number, code: string, detail: string, errors?: { pointer: string; detail: string }[]) {
  return new ApiError(status, { type: "about:blank", title: "Error", status, code, detail, errors });
}

describe("SignInForm", () => {
  it("submits the email address and password", async () => {
    const onSubmit = vi.fn();
    render(<SignInForm onSubmit={onSubmit} pending={false} error={null} />);
    await userEvent.type(screen.getByLabelText("Email address"), "ada@example.com");
    await userEvent.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(onSubmit).toHaveBeenCalledWith({ email: "ada@example.com", password: "correct horse battery staple" });
  });

  it("shows a failed sign-in and moves focus to it", () => {
    const error = problem(401, "invalid-credentials", "The email address or password is wrong.");
    render(<SignInForm onSubmit={vi.fn()} pending={false} error={error} />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("The email address or password is wrong.");
    expect(alert).toHaveFocus();
  });

  it("can't be submitted twice while signing in", () => {
    render(<SignInForm onSubmit={vi.fn()} pending={true} error={null} />);
    expect(screen.getByRole("button", { name: "Signing in…" })).toBeDisabled();
  });
});

describe("SetupForm", () => {
  it("shows each invalid field next to it and focuses the first", () => {
    const error = problem(400, "validation-failed", "The request has 2 invalid fields.", [
      { pointer: "/email", detail: "Enter an email address, such as ada@example.com." },
      { pointer: "/password", detail: "Use at least 12 characters." },
    ]);
    render(<SetupForm onSubmit={vi.fn()} pending={false} error={error} />);

    const email = screen.getByLabelText("Email address");
    expect(email).toHaveAttribute("aria-invalid", "true");
    expect(email).toHaveAccessibleDescription(/Enter an email address/);
    expect(email).toHaveFocus();
    expect(screen.getByLabelText("Password")).toHaveAccessibleDescription(/Use at least 12 characters/);
    // Every problem is next to its field, so there is no separate alert.
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("explains a wrong setup code", () => {
    const error = problem(403, "invalid-setup-code", "The setup code is wrong.");
    render(<SetupForm onSubmit={vi.fn()} pending={false} error={error} />);
    expect(screen.getByRole("alert")).toHaveTextContent("The setup code is wrong.");
  });

  it("counts down to the minimum password length", async () => {
    render(<SetupForm onSubmit={vi.fn()} pending={false} error={null} />);
    await userEvent.type(screen.getByLabelText("Password"), "short");
    expect(screen.getByText("7 more characters needed.")).toBeInTheDocument();
  });
});

describe("WorkspaceForm", () => {
  it("suggests a slug from the name until the slug is edited", async () => {
    const onSubmit = vi.fn();
    render(<WorkspaceForm onSubmit={onSubmit} pending={false} error={null} />);
    const name = screen.getByLabelText("Name");
    const slug = screen.getByLabelText("Slug");

    await userEvent.type(name, "Payments Team");
    expect(slug).toHaveValue("payments-team");

    await userEvent.clear(slug);
    await userEvent.type(slug, "pay");
    await userEvent.type(name, " EU");
    expect(slug).toHaveValue("pay");

    await userEvent.click(screen.getByRole("button", { name: "Create workspace" }));
    expect(onSubmit).toHaveBeenCalledWith({ name: "Payments Team EU", slug: "pay" });
  });

  it("puts a taken slug next to the slug field", () => {
    const error = problem(409, "slug-taken", "Another workspace already uses this slug.");
    render(<WorkspaceForm onSubmit={vi.fn()} pending={false} error={error} />);
    expect(screen.getByLabelText("Slug")).toHaveAccessibleDescription(/Another workspace uses this slug/);
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("ProjectForm", () => {
  it("submits the name and description", async () => {
    const onSubmit = vi.fn();
    render(<ProjectForm onSubmit={onSubmit} pending={false} error={null} />);
    await userEvent.type(screen.getByLabelText("Name"), "Shop");
    await userEvent.type(screen.getByLabelText("Description (optional)"), "Checkout and orders.");
    await userEvent.click(screen.getByRole("button", { name: "Create project" }));
    expect(onSubmit).toHaveBeenCalledWith({ name: "Shop", description: "Checkout and orders." });
  });
});
