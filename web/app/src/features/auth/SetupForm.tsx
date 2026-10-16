// SPDX-License-Identifier: Apache-2.0

import { type FormEvent, useRef, useState } from "react";
import { Field, FormAlert, fieldError, useFocusOnError } from "../../components/Form";

export interface SetupValues {
  setupCode: string;
  name: string;
  email: string;
  password: string;
}

const MIN_PASSWORD = 12;

/** The setup code and the first admin's details. */
export function SetupForm({
  onSubmit,
  pending,
  error,
}: {
  onSubmit: (values: SetupValues) => void;
  pending: boolean;
  error: unknown;
}) {
  const [values, setValues] = useState<SetupValues>({ setupCode: "", name: "", email: "", password: "" });
  const form = useRef<HTMLFormElement>(null);
  useFocusOnError(form, error);
  const set = (field: keyof SetupValues) => (e: { target: { value: string } }) =>
    setValues((v) => ({ ...v, [field]: e.target.value }));

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit(values);
  }

  const tooShort = values.password.length > 0 && values.password.length < MIN_PASSWORD;

  return (
    <form ref={form} className="form" onSubmit={submit} noValidate>
      <FormAlert error={error} fields={["setupCode", "name", "email", "password"]} />
      <Field
        label="Setup code"
        name="setupCode"
        autoComplete="off"
        spellCheck={false}
        required
        value={values.setupCode}
        onChange={set("setupCode")}
        className="mono"
        hint={
          <>
            The server prints it to its log when it starts. With Docker Compose, run{" "}
            <code>docker compose logs signalbox</code> and look for <code>setup_code</code>.
          </>
        }
        error={fieldError(error, "setupCode")}
      />
      <Field
        label="Your name"
        name="name"
        autoComplete="name"
        required
        value={values.name}
        onChange={set("name")}
        error={fieldError(error, "name")}
      />
      <Field
        label="Email address"
        type="email"
        name="email"
        autoComplete="email"
        required
        value={values.email}
        onChange={set("email")}
        hint="You sign in with this address."
        error={fieldError(error, "email")}
      />
      <Field
        label="Password"
        type="password"
        name="password"
        autoComplete="new-password"
        required
        minLength={MIN_PASSWORD}
        value={values.password}
        onChange={set("password")}
        hint={
          tooShort
            ? `${MIN_PASSWORD - values.password.length} more characters needed.`
            : `At least ${MIN_PASSWORD} characters. A few unrelated words make a strong password.`
        }
        error={fieldError(error, "password")}
      />
      <button type="submit" className="button primary wide" disabled={pending}>
        {pending ? "Creating account…" : "Create admin account"}
      </button>
    </form>
  );
}
