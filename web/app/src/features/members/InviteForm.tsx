// SPDX-License-Identifier: Apache-2.0

import type { WorkspaceRole } from "@signalbox/api-client";
import { type FormEvent, useId, useRef, useState } from "react";
import { Field, FormAlert, fieldError, useFocusOnError } from "../../components/Form";
import { assignableRoles, roleDescriptions, roleLabels } from "../workspaces/roles";

export interface InviteValues {
  email: string;
  role: WorkspaceRole;
}

/** Who to invite and with which role. */
export function InviteForm({
  actorRole,
  onSubmit,
  pending,
  error,
}: {
  actorRole: WorkspaceRole;
  onSubmit: (values: InviteValues) => void;
  pending: boolean;
  error: unknown;
}) {
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<WorkspaceRole>("editor");
  const form = useRef<HTMLFormElement>(null);
  const roleId = useId();
  useFocusOnError(form, error);

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit({ email, role });
  }

  return (
    <form ref={form} className="form" onSubmit={submit} noValidate>
      <FormAlert error={error} fields={["email", "role"]} />
      <Field
        label="Email address"
        type="email"
        name="email"
        autoComplete="off"
        required
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        hint="Only this address can accept the invitation."
        error={fieldError(error, "email")}
      />
      <div className="field">
        <label htmlFor={roleId}>Role</label>
        <select
          id={roleId}
          name="role"
          value={role}
          onChange={(e) => setRole(e.target.value as WorkspaceRole)}
          aria-describedby={`${roleId}-hint`}
        >
          {assignableRoles(actorRole).map((r) => (
            <option key={r} value={r}>
              {roleLabels[r]}
            </option>
          ))}
        </select>
        <p id={`${roleId}-hint`} className="field-hint">
          {roleDescriptions[role]}.
        </p>
      </div>
      <button type="submit" className="button primary" disabled={pending}>
        {pending ? "Creating link…" : "Create invitation link"}
      </button>
    </form>
  );
}
