// SPDX-License-Identifier: Apache-2.0

import type { InvitationDetails, NewInvitation, Session } from "@signalbox/api-client";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithRouter } from "../../test/router";
import { InviteView } from "../invitations/InvitePage";
import { assignableRoles, canManage } from "../workspaces/roles";
import { InvitationLink } from "./InvitationLink";
import { InviteForm } from "./InviteForm";

describe("roles", () => {
  it("lets only owners hand out the owner role", () => {
    expect(assignableRoles("owner")).toContain("owner");
    expect(assignableRoles("admin")).not.toContain("owner");
    expect(assignableRoles("editor")).toEqual([]);
  });

  it("lets admins manage everyone but owners", () => {
    expect(canManage("admin", "editor")).toBe(true);
    expect(canManage("admin", "owner")).toBe(false);
    expect(canManage("owner", "owner")).toBe(true);
    expect(canManage("viewer", "viewer")).toBe(false);
  });
});

describe("InviteForm", () => {
  it("offers the roles the inviter may give and submits the choice", async () => {
    const onSubmit = vi.fn();
    render(<InviteForm actorRole="admin" onSubmit={onSubmit} pending={false} error={null} />);
    const role = screen.getByLabelText("Role");
    expect(within(role).queryByRole("option", { name: "Owner" })).toBeNull();
    expect(role).toHaveValue("editor");

    await userEvent.type(screen.getByLabelText("Email address"), "grace@example.com");
    await userEvent.selectOptions(role, "viewer");
    expect(screen.getByText("Can see everything in the workspace.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Create invitation link" }));
    expect(onSubmit).toHaveBeenCalledWith({ email: "grace@example.com", role: "viewer" });
  });
});

describe("InvitationLink", () => {
  const invitation: NewInvitation = {
    id: "inv_01j9z3v2x4z6b8d0f2h4k6m8p0",
    workspaceId: "ws_01j9z3kq4m8r2t6v0x5w7y9a1b",
    email: "grace@example.com",
    role: "editor",
    createdAt: "2026-10-16T12:00:00Z",
    expiresAt: "2026-10-23T12:00:00Z",
    url: "https://signalbox.example.com/invite#token",
  };

  it("shows the link once, selected, and copies it", async () => {
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    const onDone = vi.fn();
    render(<InvitationLink invitation={invitation} onDone={onDone} />);

    const link = screen.getByLabelText("Invitation link");
    expect(link).toHaveValue(invitation.url);
    expect(link).toHaveFocus();
    expect(screen.getByText(/makes them an editor/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(writeText).toHaveBeenCalledWith(invitation.url);
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(onDone).toHaveBeenCalled();
  });
});

describe("InviteView", () => {
  const details: InvitationDetails = {
    workspace: { id: "ws_01j9z3kq4m8r2t6v0x5w7y9a1b", name: "Payments" },
    email: "grace@example.com",
    role: "editor",
    invitedBy: "Ada Lovelace",
    expiresAt: "2026-10-23T12:00:00Z",
    accountExists: false,
  };
  const session = (email: string): Session => ({
    user: { id: "usr_01j9z3kq4m8r2t6v0x5w7y9a1b", email, name: "Someone" },
    expiresAt: "2026-10-23T12:00:00Z",
    csrfToken: "csrf",
  });
  const actions = () => ({
    accept: { run: vi.fn(), pending: false },
    signUp: { run: vi.fn(), pending: false },
    signOut: { run: vi.fn(), pending: false },
  });

  it("lets a new person create an account", async () => {
    const a = actions();
    renderWithRouter(<InviteView details={details} session={null} token="tok" {...a} />);
    expect(await screen.findByText(/Ada Lovelace invited/)).toHaveTextContent("join Payments as an editor");
    expect(screen.getByLabelText("Email address")).toHaveValue("grace@example.com");
    await userEvent.type(screen.getByLabelText("Your name"), "Grace Hopper");
    await userEvent.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await userEvent.click(screen.getByRole("button", { name: "Create account and join" }));
    expect(a.signUp.run).toHaveBeenCalledWith({ name: "Grace Hopper", password: "correct horse battery staple" });
  });

  it("sends someone with an account to sign in and back", async () => {
    renderWithRouter(<InviteView details={{ ...details, accountExists: true }} session={null} token="tok" {...actions()} />);
    const link = await screen.findByRole("link", { name: "Sign in" });
    expect(link.getAttribute("href")).toBe("/sign-in?redirect=%2Finvite%23tok");
  });

  it("lets the right person join", async () => {
    const a = actions();
    renderWithRouter(<InviteView details={details} session={session("GRACE@example.com")} token="tok" {...a} />);
    await userEvent.click(await screen.findByRole("button", { name: "Join Payments" }));
    expect(a.accept.run).toHaveBeenCalled();
  });

  it("explains when someone else is signed in", async () => {
    const a = actions();
    renderWithRouter(<InviteView details={details} session={session("ada@example.com")} token="tok" {...a} />);
    expect(await screen.findByText(/You're signed in as/)).toHaveTextContent("ada@example.com");
    expect(screen.queryByRole("button", { name: "Join Payments" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Sign out" }));
    expect(a.signOut.run).toHaveBeenCalled();
  });
});
