// SPDX-License-Identifier: Apache-2.0

import type { Run, RunStep } from "@signalbox/api-client";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderWithRouter } from "../../test/router";
import { appendSteps, dataFormat, reportUrl, statusLabel, stepState, stepTests, stoppedReason } from "./model";
import { RunSummary, StepList, StepView } from "./RunPage";
import { RunRow } from "./RunSetupPage";

const run: Run = {
  id: "run_1",
  projectId: "prj_1",
  collectionId: "col_1",
  name: "Shop API",
  environment: "Staging",
  status: "failed",
  stopped: "bail",
  iterations: 2,
  dataRows: 2,
  delayMs: 0,
  bail: true,
  totals: { requests: 3, failedRequests: 1, tests: 4, failedTests: 1 },
  createdAt: "2026-10-28T12:00:00Z",
  startedAt: "2026-10-28T12:00:00Z",
  finishedAt: "2026-10-28T12:00:02Z",
  durationMs: 1834,
};

const passed: RunStep = {
  seq: 1,
  iteration: 0,
  name: "Log in",
  method: "POST",
  url: "https://api.test/login",
  failed: false,
  durationMs: 40,
  response: { status: 200, size: 12 },
  scripts: { tests: [{ from: "Log in", name: "is ok", passed: true }, { from: "Log in", name: "later", passed: false, skipped: true }], logs: [], errors: [], changes: [], skipped: false },
};

const failed: RunStep = {
  seq: 2,
  iteration: 1,
  name: "Create an order",
  path: "Orders",
  method: "POST",
  url: "https://api.test/orders",
  failed: true,
  durationMs: 83,
  headers: [{ key: "Authorization", value: "•••" }],
  response: { status: 500, statusText: "Internal Server Error", size: 24, headers: [{ key: "Content-Type", value: "application/json" }], body: '{"error":"out of stock"}' },
  scripts: {
    tests: [{ from: "Orders", name: "is created", passed: false, error: "AssertionError: expected 500 to equal 201" }],
    logs: [{ from: "Orders", level: "log", message: "token •••" }],
    errors: [],
    changes: [],
    skipped: false,
  },
};

const skipped: RunStep = {
  seq: 3,
  iteration: 1,
  name: "Pay",
  method: "POST",
  url: "https://api.test/pay",
  failed: false,
  durationMs: 0,
  failure: { code: "skipped", message: "A pre-request script skipped the request, so it wasn't sent." },
};

describe("model", () => {
  it("reads data files by their extension", () => {
    expect(dataFormat("users.JSON")).toBe("json");
    expect(dataFormat("users.csv")).toBe("csv");
    expect(dataFormat("users.txt")).toBe("csv");
  });

  it("names states and reasons", () => {
    expect(statusLabel("interrupted")).toBe("Interrupted");
    expect(stoppedReason("bail")).toMatch(/first failure/);
    expect(stoppedReason(undefined)).toBeNull();
    expect([passed, failed, skipped].map(stepState)).toEqual(["passed", "failed", "skipped"]);
    expect(stepTests(passed)).toEqual({ passed: 1, total: 1 });
    expect(reportUrl("run_1", "junit")).toBe("/api/v1/runs/run_1/report?format=junit");
  });

  it("adds only steps it hasn't shown", () => {
    const shown = appendSteps([], [passed]);
    expect(appendSteps(shown, [passed, failed]).map((s) => s.seq)).toEqual([1, 2]);
    expect(appendSteps(shown, [passed])).toBe(shown);
  });
});

describe("RunSummary", () => {
  it("shows the totals and marks failures", () => {
    render(<RunSummary run={run} />);
    const totals = screen.getByLabelText("Totals");
    expect(within(totals).getByText("Failed tests").parentElement).toHaveClass("total-bad");
    expect(within(totals).getByText("Requests").nextSibling).toHaveTextContent("3");
  });
});

describe("StepList", () => {
  it("lists steps with their iterations, and filters to failures", async () => {
    render(<StepList runId="run_1" steps={[passed, failed, skipped]} live={false} />);
    const list = screen.getByRole("list", { name: "Requests of the run" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(3);
    expect(within(list).getAllByText("#2 ·", { exact: false })).toHaveLength(2);
    expect(within(list).getByText("Skipped")).toBeInTheDocument();
    await userEvent.click(screen.getByLabelText("Failed only"));
    expect(within(list).getAllByRole("listitem")).toHaveLength(1);
    expect(within(list).getByText("Create an order")).toBeInTheDocument();
  });

  it("says when a live run hasn't sent anything yet", () => {
    render(<StepList runId="run_1" steps={[]} live />);
    expect(screen.getByText(/Waiting for the first request/)).toBeInTheDocument();
  });
});

describe("StepView", () => {
  it("shows a failed step's tests, console, response and request", async () => {
    render(<StepView step={failed} />);
    expect(screen.getByText("AssertionError: expected 500 to equal 201")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: "Console (1)" }));
    expect(screen.getByText(/token •••/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: "Response" }));
    expect(screen.getByText(/"error": "out of stock"/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: "Request headers" }));
    expect(screen.getByRole("table", { name: "Request headers" })).toHaveTextContent("Authorization•••");
  });

  it("says why a skipped step wasn't sent", () => {
    render(<StepView step={skipped} />);
    expect(screen.getByText(/skipped the request/)).toBeInTheDocument();
    expect(screen.getByText("No response.")).toBeInTheDocument();
  });
});

describe("RunRow", () => {
  it("links to the run with its outcome", async () => {
    renderWithRouter(
      <ul>
        <RunRow run={run} />
      </ul>,
    );
    const link = await screen.findByRole("link");
    expect(link).toHaveAttribute("href", "/runs/run_1");
    expect(link).toHaveTextContent("Failed");
    expect(link).toHaveTextContent("3/4 tests passed");
  });
});
