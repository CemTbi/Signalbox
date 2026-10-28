// SPDX-License-Identifier: Apache-2.0

import { type ChangeEvent, type FormEvent, useId, useRef, useState } from "react";
import { ApiError } from "../../api";
import { Field, FormAlert, fieldError, useFocusOnError } from "../../components/Form";
import { MAX_SLUG, slugify } from "../workspaces/slug";

export interface ServiceValues {
  name: string;
  slug: string;
}

/** A new service's name and slug. The slug follows the name until edited. */
export function ServiceForm({
  onSubmit,
  pending,
  error,
}: {
  onSubmit: (values: ServiceValues) => void;
  pending: boolean;
  error: unknown;
}) {
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  useFocusOnError(form, error);
  const slugTaken = error instanceof ApiError && error.code === "slug-taken";

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit({ name, slug });
  }

  return (
    <form ref={form} className="form" onSubmit={submit} noValidate>
      <FormAlert error={slugTaken ? null : error} fields={["name", "slug"]} />
      <Field
        label="Name"
        name="name"
        required
        maxLength={100}
        value={name}
        onChange={(e) => {
          setName(e.target.value);
          if (!slugEdited) setSlug(slugify(e.target.value));
        }}
        error={fieldError(error, "name")}
      />
      <Field
        label="Slug"
        name="slug"
        required
        maxLength={MAX_SLUG}
        spellCheck={false}
        autoCapitalize="off"
        className="mono"
        value={slug}
        onChange={(e) => {
          setSlug(e.target.value);
          setSlugEdited(true);
        }}
        hint="Names the service's mock and its URLs. Unique in this project."
        error={slugTaken ? "Another service in this project uses this slug. Choose another." : fieldError(error, "slug")}
      />
      <button type="submit" className="button primary" disabled={pending}>
        {pending ? "Adding…" : "Add service"}
      </button>
    </form>
  );
}

export interface SpecValues {
  name: string;
  content: string;
}

/** The largest document the API takes, in bytes. */
export const MAX_SPEC_BYTES = 10 * 1024 * 1024;

/**
 * A new spec: a name and an OpenAPI document, pasted or read from a file.
 * A file also suggests the name, from the document's title.
 */
export function SpecForm({
  onSubmit,
  pending,
  error,
}: {
  onSubmit: (values: SpecValues) => void;
  pending: boolean;
  error: unknown;
}) {
  const [name, setName] = useState("");
  const [content, setContent] = useState("");
  const [fileProblem, setFileProblem] = useState<string>();
  const form = useRef<HTMLFormElement>(null);
  const fileId = useId();
  useFocusOnError(form, error);
  const unreadable = error instanceof ApiError && error.code === "unreadable-document";

  async function readFile(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = ""; // choosing the same file again reads it again
    if (!file) return;
    if (file.size > MAX_SPEC_BYTES) {
      setFileProblem("The file is larger than 10 MiB.");
      return;
    }
    setFileProblem(undefined);
    const text = await file.text();
    setContent(text);
    if (!name) setName(titleOf(text) ?? file.name.replace(/\.(ya?ml|json)$/i, ""));
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit({ name, content });
  }

  return (
    <form ref={form} className="form" onSubmit={submit} noValidate>
      <FormAlert error={unreadable ? null : error} fields={["name", "content"]} />
      <Field label="Name" name="name" required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} error={fieldError(error, "name")} />
      <div className="field">
        <label htmlFor={fileId}>OpenAPI file</label>
        <input id={fileId} type="file" accept=".yaml,.yml,.json,application/yaml,application/json" onChange={(e) => void readFile(e)} />
        {fileProblem ? <p className="field-error">{fileProblem}</p> : null}
      </div>
      <Field
        multiline
        label="Document (YAML or JSON)"
        name="content"
        required
        rows={12}
        className="editor"
        spellCheck={false}
        autoCapitalize="off"
        value={content}
        onChange={(e) => setContent(e.target.value)}
        hint="Choose a file above, or paste the document. Swagger 2.0 and OpenAPI 3.0 to 3.2 work."
        error={unreadable ? describeUnreadable(error) : fieldError(error, "content")}
      />
      <button type="submit" className="button primary" disabled={pending}>
        {pending ? "Checking and saving…" : "Add spec"}
      </button>
    </form>
  );
}

function describeUnreadable(error: ApiError) {
  return `This isn't an OpenAPI or Swagger document Signalbox can read. ${error.problem.detail ?? ""}`.trim();
}

/** The info.title of a YAML or JSON document, found without a parser. */
export function titleOf(text: string): string | undefined {
  const json = /"info"\s*:\s*\{[^}]*?"title"\s*:\s*"([^"]{1,100})"/s.exec(text);
  if (json) return json[1];
  return yamlTitle(text);
}

// Line by line: one regular expression over the whole document can
// backtrack for minutes on some files.
function yamlTitle(text: string): string | undefined {
  const lines = text.split(/\r?\n/);
  const start = lines.findIndex((line) => /^info:[ \t]*$/.test(line));
  if (start < 0) return undefined;
  let indent: number | undefined;
  for (const line of lines.slice(start + 1)) {
    const key = line.trimStart();
    if (key === "" || key.startsWith("#")) continue;
    const depth = line.length - key.length;
    if (depth === 0) return undefined; // past the end of info
    indent ??= depth;
    // Deeper lines belong to info's own objects, such as contact.
    if (depth === indent && key.startsWith("title:")) return scalar(key.slice("title:".length));
  }
  return undefined;
}

/** A one-line YAML value without its quotes or comment, if it has 1 to 100 characters. */
function scalar(raw: string): string | undefined {
  const text = raw.trim();
  let value: string | undefined;
  if (text.startsWith("'")) {
    // Inside single quotes, '' is a quote.
    value = /^'((?:[^']|'')*)'/.exec(text)?.[1]?.replaceAll("''", "'");
  } else if (text.startsWith('"')) {
    value = /^"((?:[^"\\]|\\.)*)"/.exec(text)?.[1]?.replace(/\\(["\\])/g, "$1");
  } else {
    value = text.includes(" #") ? text.slice(0, text.indexOf(" #")).trimEnd() : text;
  }
  return value && value.length <= 100 ? value : undefined;
}
