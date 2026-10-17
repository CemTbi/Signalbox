// SPDX-License-Identifier: Apache-2.0

import type { ReferenceSchema } from "@signalbox/api-client";
import { Markdown } from "./Markdown";

/** The anchor of a component schema's section. */
export function schemaAnchor(name: string) {
  return `schema-${name}`;
}

/** A schema's type in a few words, such as "array of Order" or "string (date-time)". */
export function typeLabel(s: ReferenceSchema | undefined): string {
  if (!s) return "any";
  if (s.ref && s.name) return s.name;
  let label: string;
  if (s.types?.includes("array") && s.items) label = `array of ${typeLabel(s.items)}`;
  else if (s.types && s.types.length > 0) label = s.types.join(" or ");
  else if (s.oneOf) label = "one of";
  else if (s.anyOf) label = "any of";
  else if (s.allOf) label = s.allOf.length === 1 ? typeLabel(s.allOf[0]) : "all of";
  else if (s.properties) label = "object";
  else label = "any";
  if (s.format) label += ` (${s.format})`;
  if (s.nullable) label += " or null";
  return label;
}

function value(v: unknown) {
  return typeof v === "string" ? v : JSON.stringify(v);
}

/** What a schema allows, beyond its type: limits, allowed values and its default. */
function Facts({ schema: s }: { schema: ReferenceSchema }) {
  const facts: string[] = [...(s.constraints ?? [])];
  if (s.readOnly) facts.push("read-only");
  if (s.writeOnly) facts.push("write-only");
  if (s.deprecated) facts.push("deprecated");
  return (
    <>
      {facts.length > 0 ? (
        <ul className="schema-facts" aria-label="Limits">
          {facts.map((f) => (
            <li key={f}>{f}</li>
          ))}
        </ul>
      ) : null}
      {s.enum ? (
        <p className="schema-line">
          One of:{" "}
          {s.enum.map((v, i) => (
            <span key={i}>
              {i > 0 ? ", " : ""}
              <code>{value(v)}</code>
            </span>
          ))}
        </p>
      ) : null}
      {s.const !== undefined ? (
        <p className="schema-line">
          Always <code>{value(s.const)}</code>
        </p>
      ) : null}
      {s.default !== undefined ? (
        <p className="schema-line">
          Default <code>{value(s.default)}</code>
        </p>
      ) : null}
    </>
  );
}

/** The schemas a schema is made of: properties, items, and combinations. */
function Parts({ schema: s, depth }: { schema: ReferenceSchema; depth: number }) {
  const items = s.items && !s.items.ref && hasParts(s.items) ? s.items : undefined;
  const combos: [string, ReferenceSchema[] | undefined][] = [
    ["All of", s.allOf],
    ["One of", s.oneOf],
    ["Any of", s.anyOf],
  ];
  return (
    <>
      {s.properties && s.properties.length > 0 ? (
        <ul className="props">
          {s.properties.map((p) => (
            <li key={p.name} className="prop">
              <div className="prop-head">
                <code className="prop-name">{p.name}</code>
                <TypeName schema={p.schema} />
                {p.required ? <span className="prop-required">required</span> : null}
              </div>
              <SchemaBody schema={p.schema} depth={depth + 1} />
            </li>
          ))}
        </ul>
      ) : null}
      {s.additionalProperties ? (
        <div className="schema-part">
          <p className="schema-line">
            Other keys: <TypeName schema={s.additionalProperties} />
          </p>
          <SchemaBody schema={s.additionalProperties} depth={depth + 1} />
        </div>
      ) : null}
      {items ? (
        <div className="schema-part">
          <p className="schema-line">Each item:</p>
          <SchemaBody schema={items} depth={depth + 1} />
        </div>
      ) : null}
      {combos.map(([label, list]) =>
        list && list.length > 0 && !(label === "All of" && list.length === 1 && !s.properties) ? (
          <div key={label} className="schema-part">
            <p className="schema-line">
              {label}
              {s.discriminator && label !== "All of" ? (
                <>
                  , chosen by <code>{s.discriminator}</code>
                </>
              ) : null}
              :
            </p>
            <ol className="variants">
              {list.map((v, i) => (
                <li key={i}>
                  <TypeName schema={v} />
                  <SchemaBody schema={v} depth={depth + 1} />
                </li>
              ))}
            </ol>
          </div>
        ) : null,
      )}
      {s.allOf?.length === 1 && !s.properties ? <SchemaBody schema={s.allOf[0]!} depth={depth} /> : null}
    </>
  );
}

function hasParts(s: ReferenceSchema): boolean {
  return Boolean(s.properties?.length || s.allOf?.length || s.oneOf?.length || s.anyOf?.length || s.additionalProperties || (s.items && hasParts(s.items)));
}

/** A schema's type, linking to the component it names. */
export function TypeName({ schema }: { schema: ReferenceSchema | undefined }) {
  const label = typeLabel(schema);
  const name = schema?.ref ? schema.name : schema?.items?.ref ? schema.items.name : undefined;
  if (name) {
    return (
      <a className="type-name" href={`#${schemaAnchor(name)}`}>
        {label}
      </a>
    );
  }
  return <span className="type-name">{label}</span>;
}

/**
 * A schema's description, facts and parts. Nested objects start folded, so
 * a large body stays readable; the top level is open.
 */
export function SchemaBody({ schema, depth = 0 }: { schema: ReferenceSchema | undefined; depth?: number }) {
  if (!schema || schema.ref) return null;
  const parts = hasParts(schema);
  return (
    <div className="schema">
      <Markdown source={schema.description} className="schema-description" />
      <Facts schema={schema} />
      {parts && depth > 0 ? (
        <details className="schema-nested">
          <summary>{schema.properties ? `${schema.properties.length} ${schema.properties.length === 1 ? "property" : "properties"}` : "Details"}</summary>
          <Parts schema={schema} depth={depth} />
        </details>
      ) : parts ? (
        <Parts schema={schema} depth={depth} />
      ) : null}
    </div>
  );
}
