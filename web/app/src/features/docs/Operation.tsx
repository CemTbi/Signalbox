// SPDX-License-Identifier: Apache-2.0

import type { ReferenceMedia, ReferenceOperation, ReferenceParameter, ReferenceSecurityScheme } from "@signalbox/api-client";
import { useId, useState } from "react";
import { Markdown } from "./Markdown";
import { type Language, languages, sample, sampleRequest } from "./samples";
import { SchemaBody, TypeName } from "./SchemaView";
import { TryIt } from "./TryIt";

/** Where an operation's section is, for links. */
export function operationAnchor(group: number, op: ReferenceOperation) {
  return `op-${group}-${op.id.replace(/[^A-Za-z0-9_-]+/g, "-")}`;
}

export interface Target {
  /** Where samples send requests: the mock or one of the API's servers. */
  base: string;
  /** The mock's address, which Try it sends to. */
  mock: string;
  schemes: ReferenceSecurityScheme[];
  language: Language;
  onLanguage: (l: Language) => void;
}

export function Operation({ op, anchor, target }: { op: ReferenceOperation; anchor: string; target: Target }) {
  const [trying, setTrying] = useState(false);
  const headingId = `${anchor}-title`;
  return (
    <section id={anchor} aria-labelledby={headingId} className="op">
      <div className="op-main">
        <h3 id={headingId} className="op-title">
          {op.summary || `${op.method} ${op.path}`}
          {op.deprecated ? <span className="badge">Deprecated</span> : null}
        </h3>
        <p className="op-line">
          <span className={`method method-${op.method.toLowerCase()}`}>{op.method}</span> <code className="op-path">{op.path}</code>
        </p>
        <Markdown source={op.description} />
        <Security op={op} schemes={target.schemes} />
        {op.parameters && op.parameters.length > 0 ? <Parameters params={op.parameters} /> : null}
        {op.requestBody ? (
          <div className="op-part">
            <h4>
              Request body{op.requestBody.required ? <span className="prop-required">required</span> : null}
            </h4>
            <Markdown source={op.requestBody.description} />
            <Bodies content={op.requestBody.content} />
          </div>
        ) : null}
        <div className="op-part">
          <h4>Responses</h4>
          <ul className="responses">
            {op.responses.map((r) => (
              <li key={r.code} className="response">
                <details open={op.responses.length === 1}>
                  <summary>
                    <span className={`status status-${r.code.charAt(0)}`}>{r.code}</span> {firstLine(r.description)}
                  </summary>
                  <Markdown source={r.description} />
                  {r.headers && r.headers.length > 0 ? (
                    <>
                      <h5>Headers</h5>
                      <Parameters params={r.headers} headers />
                    </>
                  ) : null}
                  {r.content ? <Bodies content={r.content} /> : null}
                </details>
              </li>
            ))}
          </ul>
        </div>
      </div>
      <div className="op-side">
        <CodeSamples op={op} target={target} />
        <button type="button" className="button secondary" aria-expanded={trying} onClick={() => setTrying(!trying)}>
          {trying ? "Close Try it" : "Try it against the mock"}
        </button>
        {trying ? <TryIt op={op} mock={target.mock} schemes={target.schemes} /> : null}
      </div>
    </section>
  );
}

function firstLine(s: string | undefined) {
  const line = (s ?? "").split("\n")[0] ?? "";
  // The summary line shows plain text; the full description follows.
  return line.replace(/[*_`]/g, "");
}

function Security({ op, schemes }: { op: ReferenceOperation; schemes: ReferenceSecurityScheme[] }) {
  if (!op.security || op.security.length === 0) return null;
  const ways = op.security.map((way) => (way.length === 0 ? "no credentials" : way.map((u) => u.scheme + (u.scopes?.length ? ` (${u.scopes.join(", ")})` : "")).join(" and ")));
  const known = new Set(schemes.map((s) => s.name));
  return (
    <p className="op-security">
      Authorize with {ways.join(", or ")}.
      {op.security.flat().some((u) => known.has(u.scheme)) ? (
        <>
          {" "}
          <a href="#authorization">How</a>
        </>
      ) : null}
    </p>
  );
}

function Parameters({ params, headers = false }: { params: ReferenceParameter[]; headers?: boolean }) {
  return (
    <div className="op-part">
      {headers ? null : <h4>Parameters</h4>}
      <ul className="props">
        {params.map((p) => (
          <li key={`${p.in} ${p.name}`} className="prop">
            <div className="prop-head">
              <code className="prop-name">{p.name}</code>
              <TypeName schema={p.schema} />
              {headers ? null : <span className="prop-in">{p.in}</span>}
              {p.required ? <span className="prop-required">required</span> : null}
              {p.deprecated ? <span className="badge">Deprecated</span> : null}
            </div>
            <Markdown source={p.description} />
            <SchemaBody schema={p.schema} depth={1} />
          </li>
        ))}
      </ul>
    </div>
  );
}

/** A body in each of its media types, with its schema and examples. */
function Bodies({ content }: { content: ReferenceMedia[] }) {
  const [chosen, setChosen] = useState(0);
  const id = useId();
  const media = content[chosen] ?? content[0];
  if (!media) return null;
  return (
    <div className="bodies">
      {content.length > 1 ? (
        <div className="media-choice">
          <label htmlFor={id} className="field-label">
            Media type
          </label>
          <select id={id} value={chosen} onChange={(e) => setChosen(Number(e.target.value))}>
            {content.map((m, i) => (
              <option key={m.type} value={i}>
                {m.type}
              </option>
            ))}
          </select>
        </div>
      ) : (
        <p className="schema-line">
          <code>{media.type}</code>
          {media.schema ? (
            <>
              {" "}
              · <TypeName schema={media.schema} />
            </>
          ) : null}
        </p>
      )}
      <SchemaBody schema={media.schema} />
      {media.examples?.map((ex, i) => (
        <figure key={ex.name ?? i} className="example">
          <figcaption>{ex.summary || (ex.name ? `Example ${ex.name}` : "Example")}</figcaption>
          <Markdown source={ex.description} />
          <pre className="code" tabIndex={0}>
            <code>{ex.value}</code>
          </pre>
        </figure>
      ))}
    </div>
  );
}

function CodeSamples({ op, target }: { op: ReferenceOperation; target: Target }) {
  const code = sample(target.language, sampleRequest(op, target.base, target.schemes));
  const [copied, setCopied] = useState(false);
  return (
    <div className="samples">
      <div className="sample-tabs" role="group" aria-label="Language">
        {languages.map((l) => (
          <button
            key={l.id}
            type="button"
            aria-pressed={l.id === target.language}
            onClick={() => {
              target.onLanguage(l.id);
              setCopied(false);
            }}
          >
            {l.label}
          </button>
        ))}
      </div>
      <pre className="code" tabIndex={0}>
        <code>{code}</code>
      </pre>
      <button
        type="button"
        className="button secondary small"
        onClick={() => {
          void navigator.clipboard?.writeText(code).then(() => setCopied(true));
        }}
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}
