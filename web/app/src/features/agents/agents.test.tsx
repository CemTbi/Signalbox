// SPDX-License-Identifier: Apache-2.0

import type { Agent } from "@signalbox/api-client";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithRouter } from "../../test/router";
import { rememberAgent, rememberedAgent, RunOnSelect } from "./RunOn";

const laptop: Agent = { id: "agt_1", name: "laptop", online: true, createdAt: "2026-10-28T12:00:00Z", lastSeenAt: "2026-10-28T12:05:00Z" };
const desktop: Agent = { id: "agt_2", name: "desktop", online: false, createdAt: "2026-10-28T12:00:00Z", lastSeenAt: "2026-10-27T09:00:00Z" };

describe("RunOnSelect", () => {
  it("stays out of the way until you have an agent", () => {
    const { container } = render(<RunOnSelect agent={undefined} agents={[]} onChange={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("offers the server and your agents, and says which are offline", async () => {
    const onChange = vi.fn();
    renderWithRouter(<RunOnSelect agent={undefined} agents={[laptop, desktop]} onChange={onChange} />);
    const select = await screen.findByLabelText("Run on");
    expect(select).toHaveValue("");
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["Server", "laptop", "desktop (offline)"]);
    await userEvent.selectOptions(select, "laptop");
    expect(onChange).toHaveBeenCalledWith("agt_1");
  });

  it("says how to start an offline agent that's chosen", async () => {
    renderWithRouter(<RunOnSelect agent={desktop} agents={[laptop, desktop]} onChange={vi.fn()} />);
    expect(await screen.findByLabelText("Run on")).toHaveAccessibleDescription(/desktop isn't running\. Start signalbox agent there/);
    expect(screen.getByRole("link", { name: "Agents" })).toHaveAttribute("href", "/account/agents");
  });
});

describe("rememberAgent", () => {
  it("keeps the choice across pages", () => {
    rememberAgent("agt_1");
    expect(rememberedAgent()).toBe("agt_1");
    rememberAgent("");
    expect(rememberedAgent()).toBe("");
  });
});
