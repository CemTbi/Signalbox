// SPDX-License-Identifier: Apache-2.0

import type { Revision, Version } from "@signalbox/api-client";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { ApiError } from "../../api";
import { Field, FormAlert, fieldError, useFocusOnError } from "../../components/Form";

export interface PublishValues {
  name: string;
  notes: string;
}

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

/**
 * A spec's published versions, and a form that publishes the current
 * revision. A revision with errors can't be published, so the form says so
 * instead of offering a button that would fail.
 */
export function Versions({
  versions,
  current,
  canPublish,
  pending,
  error,
  onPublish,
}: {
  versions: Version[];
  /** The spec's current revision. */
  current: Revision | undefined;
  canPublish: boolean;
  pending: boolean;
  error: unknown;
  onPublish: (values: PublishValues) => void;
}) {
  const [name, setName] = useState(current?.apiVersion ?? "");
  const [notes, setNotes] = useState("");
  const form = useRef<HTMLFormElement>(null);
  const headingId = useId();
  useFocusOnError(form, error);

  // A new current revision suggests its own version.
  useEffect(() => {
    setName(current?.apiVersion ?? "");
    setNotes("");
  }, [current?.id, current?.apiVersion]);

  const published = current ? versions.find((v) => v.revisionId === current.id) : undefined;
  const errors = current?.counts.errors ?? 0;
  const taken = error instanceof ApiError && error.code === "version-exists";

  function submit(e: FormEvent) {
    e.preventDefault();
    onPublish({ name, notes });
  }

  return (
    <section aria-labelledby={headingId} className="split">
      <div className="split-main">
        <h2 id={headingId} className="section-title">
          Versions
        </h2>
        {versions.length === 0 ? (
          <div className="empty-state">
            <p className="empty-title">Nothing published yet</p>
            <p>Mocks, docs and generated code use published versions. Publish one when the spec is ready to share.</p>
          </div>
        ) : (
          <ol className="rows">
            {versions.map((v) => (
              <li key={v.id} className="row">
                <span className="row-main">
                  <span className="row-title">
                    <span className="mono">{v.name}</span> · revision {v.revisionNumber}
                  </span>
                  <span className="row-sub">
                    Published {dateFormat.format(new Date(v.publishedAt))}
                    {v.notes ? ` · ${v.notes}` : ""}
                  </span>
                </span>
              </li>
            ))}
          </ol>
        )}
      </div>
      {canPublish && current ? (
        <div className="panel split-side">
          <h3 className="section-title">Publish revision {current.number}</h3>
          {errors > 0 ? (
            <p className="notice">
              Revision {current.number} has {errors} {errors === 1 ? "error" : "errors"}. Fix {errors === 1 ? "it" : "them"} and save
              a new revision to publish.
            </p>
          ) : published ? (
            <p className="notice">
              Revision {current.number} is published as <span className="mono">{published.name}</span>. Save a new revision to publish
              changes.
            </p>
          ) : (
            <form ref={form} className="form" onSubmit={submit} noValidate>
              <FormAlert error={taken ? null : error} fields={["name", "notes"]} />
              <Field
                label="Version"
                name="name"
                required
                maxLength={64}
                className="mono"
                value={name}
                onChange={(e) => setName(e.target.value)}
                hint="Such as 1.4.0. It comes from the document's info.version."
                error={taken ? "This spec already has a version with this name." : fieldError(error, "name")}
              />
              <Field
                multiline
                label="Release notes (optional)"
                name="notes"
                rows={3}
                maxLength={2000}
                value={notes}
                onChange={(e) => setNotes(e.target.value)}
                error={fieldError(error, "notes")}
              />
              <button type="submit" className="button primary" disabled={pending}>
                {pending ? "Publishing…" : `Publish ${name || "version"}`}
              </button>
            </form>
          )}
        </div>
      ) : null}
    </section>
  );
}
