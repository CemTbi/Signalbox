// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../../api";
import { NewToken, TokenForm } from "./TokensPage";

describe("TokenForm", () => {
  it("sends the name and the chosen lifetime", async () => {
    const onSubmit = vi.fn();
    render(<TokenForm pending={false} error={null} onSubmit={onSubmit} />);
    await userEvent.type(screen.getByLabelText("Name"), "GitHub Actions");
    await userEvent.selectOptions(screen.getByLabelText("Expires after"), "1 year");
    await userEvent.click(screen.getByRole("button", { name: "Create token" }));
    expect(onSubmit).toHaveBeenCalledWith({ name: "GitHub Actions", expiresInDays: 366 });
  });

  it("names a problem at its field", () => {
    const error = new ApiError(400, {
      type: "about:blank",
      title: "Bad Request",
      status: 400,
      code: "validation-failed",
      errors: [{ pointer: "/name", code: "required", detail: "Name the token after where it's used." }],
    });
    render(<TokenForm pending={false} error={error} onSubmit={vi.fn()} />);
    expect(screen.getByLabelText("Name")).toHaveAccessibleDescription(/Name the token after where it's used/);
  });
});

describe("NewToken", () => {
  it("shows the token once, selected and ready to copy", async () => {
    const writeText = vi.fn(async () => {});
    Object.assign(navigator, { clipboard: { writeText } });
    const onDone = vi.fn();
    render(
      <NewToken
        token={{ id: "tok_1", name: "CI", hint: "x9Qa", createdAt: "2026-10-18T12:00:00Z", expiresAt: "2027-01-16T12:00:00Z", expired: false, token: "sbx_pat_secret" }}
        onDone={onDone}
      />,
    );
    const input = screen.getByLabelText("Token");
    expect(input).toHaveValue("sbx_pat_secret");
    expect(input).toHaveFocus();
    expect(screen.getByRole("status")).toHaveTextContent("you won't see it again");
    await userEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(writeText).toHaveBeenCalledWith("sbx_pat_secret");
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(onDone).toHaveBeenCalled();
  });
});
