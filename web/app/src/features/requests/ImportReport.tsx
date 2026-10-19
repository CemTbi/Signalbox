// SPDX-License-Identifier: Apache-2.0

import type { components } from "@signalbox/api-client";

/** What an import found, as the API reports it. */
export type Report = components["schemas"]["ImportReport"];

const levelLabels: Record<Report["notes"][number]["level"], string> = {
  differs: "Runs differently",
  omitted: "Left out",
  info: "Note",
};

function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`;
}

/** What an import found: counts, then each note, the ones that matter first. */
export function ImportReport({ report, what }: { report: Report; what: "collection" | "environment" | "command" }) {
  const counts = [
    report.folders ? plural(report.folders, "folder", "folders") : "",
    report.requests && what !== "command" ? plural(report.requests, "request", "requests") : "",
    report.variables ? plural(report.variables, "variable", "variables") : "",
  ].filter(Boolean);
  const order = { omitted: 0, differs: 1, info: 2 } as const;
  const notes = [...report.notes].sort((a, b) => order[a.level] - order[b.level]);
  return (
    <div className="import-report" aria-label="Import report" role="group">
      <p className={`import-summary ${report.clean ? "clean" : "unclean"}`}>
        {report.clean
          ? "Clean: nothing is left out, and nothing will run differently."
          : "Some things will run differently or were left out. They're listed below."}
        {counts.length ? ` ${counts.join(", ")}.` : ""}
      </p>
      {notes.length ? (
        <ul className="import-notes">
          {notes.map((n, i) => (
            <li key={i} className={`import-note import-${n.level}`}>
              <span className="import-level">{levelLabels[n.level]}</span>
              {n.item ? <span className="import-item">{n.item}</span> : null}
              <span>{n.message}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
