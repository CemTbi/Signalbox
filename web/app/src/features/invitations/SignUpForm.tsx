// SPDX-License-Identifier: Apache-2.0

import { type FormEvent, useRef, useState } from "react";
import { Field, FormAlert, fieldError, useFocusOnError } from "../../components/Form";

export interface SignUpValues {
  name: string;
  password: string;
}

const MIN_PASSWORD = 12;

/** A new account's name and password; the invitation fixes the email address. */
export function SignUpForm({
  email,
  onSubmit,
  pending,
  error,
}: {
  email: string;
  onSubmit: (values: SignUpValues) => void;
  pending: boolean;
  error: unknown;
}) {
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const form = useRef<HTMLFormElement>(null);
  useFocusOnError(form, error);

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit({ name, password });
  }

  return (
    <form ref={form} className="form" onSubmit={submit} noValidate>
      <FormAlert error={error} fields={["name", "password"]} />
      <Field label="Email address" type="email" name="email" autoComplete="username" value={email} readOnly />
      <Field
        label="Your name"
        name="name"
        autoComplete="name"
        required
        value={name}
        onChange={(e) => setName(e.target.value)}
        error={fieldError(error, "name")}
      />
      <Field
        label="Password"
        type="password"
        name="password"
        autoComplete="new-password"
        required
        minLength={MIN_PASSWORD}
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        hint={`At least ${MIN_PASSWORD} characters. A few unrelated words make a strong password.`}
        error={fieldError(error, "password")}
      />
      <button type="submit" className="button primary wide" disabled={pending}>
        {pending ? "Creating account…" : "Create account and join"}
      </button>
    </form>
  );
}
