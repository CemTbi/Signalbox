// SPDX-License-Identifier: Apache-2.0

import {
  type InputHTMLAttributes,
  type ReactNode,
  type RefObject,
  type TextareaHTMLAttributes,
  useEffect,
  useId,
} from "react";
import { ApiError, describeError } from "../api";

interface FieldOwnProps {
  label: string;
  hint?: ReactNode;
  error?: string;
}

type InputFieldProps = FieldOwnProps & InputHTMLAttributes<HTMLInputElement> & { multiline?: false };
type TextAreaFieldProps = FieldOwnProps & TextareaHTMLAttributes<HTMLTextAreaElement> & { multiline: true };

/**
 * A labelled input with an optional hint and error. The hint and error are
 * linked to the input, so screen readers read them with it.
 */
export function Field(props: InputFieldProps | TextAreaFieldProps) {
  const generated = useId();
  const { label, hint, error, id = generated, multiline, ...rest } = props;
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hint ? hintId : "", error ? errorId : ""].filter(Boolean).join(" ") || undefined;
  const common = { id, "aria-invalid": error ? true : undefined, "aria-describedby": describedBy };

  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {multiline ? (
        <textarea {...common} {...(rest as TextareaHTMLAttributes<HTMLTextAreaElement>)} />
      ) : (
        <input {...common} {...(rest as InputHTMLAttributes<HTMLInputElement>)} />
      )}
      {hint ? (
        <p id={hintId} className="field-hint">
          {hint}
        </p>
      ) : null}
      {error ? (
        <p id={errorId} className="field-error">
          {error}
        </p>
      ) : null}
    </div>
  );
}

/** The message for a field from a failed request, if the server named it. */
export function fieldError(error: unknown, field: string): string | undefined {
  return error instanceof ApiError ? error.fieldError(field) : undefined;
}

/**
 * Explains a failed request. Problems that only concern named fields are
 * shown next to those fields instead.
 */
export function FormAlert({ error, fields }: { error: unknown; fields: readonly string[] }) {
  if (!error) return null;
  if (error instanceof ApiError && error.code === "validation-failed") {
    const named = error.problem.errors ?? [];
    if (named.length > 0 && named.every((e) => fields.includes(e.pointer.slice(1)))) {
      return null;
    }
  }
  return (
    <p className="form-alert" role="alert">
      {describeError(error)}
    </p>
  );
}

/**
 * Moves focus to the first invalid field after a failed submit, or to the
 * alert when no field is to blame, so keyboard and screen-reader users land
 * on the problem.
 */
export function useFocusOnError(form: RefObject<HTMLFormElement | null>, error: unknown) {
  useEffect(() => {
    if (!error || !form.current) return;
    const target =
      form.current.querySelector<HTMLElement>('[aria-invalid="true"]') ??
      form.current.querySelector<HTMLElement>('[role="alert"]');
    if (target) {
      if (!target.hasAttribute("tabindex") && target.getAttribute("role") === "alert") {
        target.setAttribute("tabindex", "-1");
      }
      target.focus();
    }
  }, [form, error]);
}
