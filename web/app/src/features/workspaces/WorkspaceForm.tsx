// SPDX-License-Identifier: Apache-2.0

import { type FormEvent, useRef, useState } from "react";
import { Field, FormAlert, fieldError, useFocusOnError } from "../../components/Form";
import { ApiError } from "../../api";
import { MAX_SLUG, slugify } from "./slug";

export interface WorkspaceValues {
  name: string;
  slug: string;
}

/**
 * A new workspace's name and slug. The slug follows the name until someone
 * edits it.
 */
export function WorkspaceForm({
  onSubmit,
  pending,
  error,
}: {
  onSubmit: (values: WorkspaceValues) => void;
  pending: boolean;
  error: unknown;
}) {
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  useFocusOnError(form, error);

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit({ name, slug });
  }

  const slugTaken = error instanceof ApiError && error.code === "slug-taken";

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
        hint="A short name for URLs and the API: lowercase letters, digits and hyphens. Unique in this installation."
        error={slugTaken ? "Another workspace uses this slug. Choose another." : fieldError(error, "slug")}
      />
      <button type="submit" className="button primary" disabled={pending}>
        {pending ? "Creating…" : "Create workspace"}
      </button>
    </form>
  );
}
