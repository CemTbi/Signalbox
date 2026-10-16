// SPDX-License-Identifier: Apache-2.0

import { type FormEvent, useRef, useState } from "react";
import { Field, FormAlert, fieldError, useFocusOnError } from "../../components/Form";

export interface SignInValues {
  email: string;
  password: string;
}

/** Email and password, for signing in. */
export function SignInForm({
  onSubmit,
  pending,
  error,
}: {
  onSubmit: (values: SignInValues) => void;
  pending: boolean;
  error: unknown;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const form = useRef<HTMLFormElement>(null);
  useFocusOnError(form, error);

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit({ email, password });
  }

  return (
    <form ref={form} className="form" onSubmit={submit} noValidate>
      <FormAlert error={error} fields={["email", "password"]} />
      <Field
        label="Email address"
        type="email"
        name="email"
        autoComplete="username"
        required
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        error={fieldError(error, "email")}
      />
      <Field
        label="Password"
        type="password"
        name="password"
        autoComplete="current-password"
        required
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        error={fieldError(error, "password")}
      />
      <button type="submit" className="button primary wide" disabled={pending}>
        {pending ? "Signing in…" : "Sign in"}
      </button>
    </form>
  );
}
