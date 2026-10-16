// SPDX-License-Identifier: Apache-2.0

import { type FormEvent, useRef, useState } from "react";
import { Field, FormAlert, fieldError, useFocusOnError } from "../../components/Form";

export interface ProjectValues {
  name: string;
  description: string;
}

/** A new project's name and description. */
export function ProjectForm({
  onSubmit,
  pending,
  error,
}: {
  onSubmit: (values: ProjectValues) => void;
  pending: boolean;
  error: unknown;
}) {
  const [values, setValues] = useState<ProjectValues>({ name: "", description: "" });
  const form = useRef<HTMLFormElement>(null);
  useFocusOnError(form, error);

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit(values);
  }

  return (
    <form ref={form} className="form" onSubmit={submit} noValidate>
      <FormAlert error={error} fields={["name", "description"]} />
      <Field
        label="Name"
        name="name"
        required
        maxLength={100}
        value={values.name}
        onChange={(e) => setValues((v) => ({ ...v, name: e.target.value }))}
        hint="One system, such as a product or a platform. It gets its own canvas."
        error={fieldError(error, "name")}
      />
      <Field
        multiline
        label="Description (optional)"
        name="description"
        rows={3}
        maxLength={1000}
        value={values.description}
        onChange={(e) => setValues((v) => ({ ...v, description: e.target.value }))}
        error={fieldError(error, "description")}
      />
      <button type="submit" className="button primary" disabled={pending}>
        {pending ? "Creating…" : "Create project"}
      </button>
    </form>
  );
}
