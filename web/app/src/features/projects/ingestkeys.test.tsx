// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { exporterSettings, KeyForm, NewKey } from "./IngestKeysPage";

describe("exporterSettings", () => {
  it("sends OTLP over HTTP to port 4318 with the key, URL-encoded", () => {
    expect(exporterSettings("sbx_ing_k", "signalbox.example.com")).toBe(
      [
        "OTEL_EXPORTER_OTLP_ENDPOINT=http://signalbox.example.com:4318",
        "OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
        "OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20sbx_ing_k",
      ].join("\n"),
    );
  });
});

describe("KeyForm", () => {
  it("sends the name", async () => {
    const onSubmit = vi.fn();
    render(<KeyForm pending={false} error={null} onSubmit={onSubmit} />);
    await userEvent.type(screen.getByLabelText("Name"), "checkout-service");
    await userEvent.click(screen.getByRole("button", { name: "Create key" }));
    expect(onSubmit).toHaveBeenCalledWith("checkout-service");
  });
});

describe("NewKey", () => {
  it("shows the key once with the settings that use it", async () => {
    const writeText = vi.fn(async () => {});
    Object.assign(navigator, { clipboard: { writeText } });
    const onDone = vi.fn();
    render(
      <NewKey
        ingestKey={{
          id: "ing_1",
          environmentId: "env_1",
          name: "checkout-service",
          hint: "x9Qa",
          createdBy: null,
          createdAt: "2026-10-30T12:00:00Z",
          lastUsedAt: null,
          key: "sbx_ing_secret",
        }}
        onDone={onDone}
      />,
    );
    const input = screen.getByLabelText("Key");
    expect(input).toHaveValue("sbx_ing_secret");
    expect(input).toHaveFocus();
    expect(screen.getByLabelText("OpenTelemetry settings")).toHaveValue(exporterSettings("sbx_ing_secret", window.location.hostname));
    await userEvent.click(screen.getByRole("button", { name: "Copy settings" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Bearer%20sbx_ing_secret"));
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(onDone).toHaveBeenCalled();
  });
});
