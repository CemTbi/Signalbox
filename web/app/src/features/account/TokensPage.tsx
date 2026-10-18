// SPDX-License-Identifier: Apache-2.0

import type { AccessToken, NewAccessToken } from "@signalbox/api-client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { Field, FormAlert, fieldError } from "../../components/Form";
import { LoadError, Loading, PageHeader } from "../../components/Page";

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
const tokensKey = ["tokens"] as const;

const lifetimes = [
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
  { days: 180, label: "180 days" },
  { days: 366, label: "1 year" },
];

/**
 * Personal access tokens, for the CLI, the agent and scripts. A token acts
 * as its person, so only the web app can make or revoke one.
 */
export function TokensPage() {
  const queryClient = useQueryClient();
  const tokens = useQuery({
    queryKey: tokensKey,
    queryFn: () => unwrap(api.GET("/api/v1/tokens")),
  });
  const [created, setCreated] = useState<NewAccessToken | null>(null);
  const create = useMutation({
    mutationFn: (body: { name: string; expiresInDays: number }) => unwrap(api.POST("/api/v1/tokens", { body })),
    onSuccess: async (t) => {
      setCreated(t);
      await queryClient.invalidateQueries({ queryKey: tokensKey });
    },
  });
  const revoke = useMutation({
    mutationFn: (tokenId: string) => unwrap(api.DELETE("/api/v1/tokens/{tokenId}", { params: { path: { tokenId } } })),
    onSettled: () => queryClient.invalidateQueries({ queryKey: tokensKey }),
  });

  return (
    <div className="stack">
      <PageHeader title="Access tokens" meta={<p className="lede">Tokens let the Signalbox CLI, the agent and your scripts act as you.</p>} />
      <div className="split">
        <section aria-labelledby="tokens" className="split-main">
          <h2 id="tokens" className="section-title">
            Your tokens
          </h2>
          {tokens.isPending ? <Loading label="Loading tokens…" /> : null}
          {tokens.isError ? <LoadError message={describeError(tokens.error)} onRetry={() => void tokens.refetch()} /> : null}
          {revoke.isError ? (
            <p className="form-alert" role="alert">
              The token couldn't be revoked: {describeError(revoke.error)}
            </p>
          ) : null}
          {tokens.data && tokens.data.data.length === 0 ? (
            <div className="empty-state">
              <p className="empty-title">No tokens yet</p>
              <p>Create one for each place you use Signalbox from, so you can revoke it without affecting the others.</p>
            </div>
          ) : null}
          {tokens.data && tokens.data.data.length > 0 ? (
            <ul className="rows">
              {tokens.data.data.map((t) => (
                <TokenRow key={t.id} token={t} revoking={revoke.isPending && revoke.variables === t.id} onRevoke={() => revoke.mutate(t.id)} />
              ))}
            </ul>
          ) : null}
        </section>
        <section aria-labelledby="new-token" className="panel split-side">
          <h2 id="new-token" className="section-title">
            New token
          </h2>
          {created ? (
            <NewToken token={created} onDone={() => setCreated(null)} />
          ) : (
            <TokenForm pending={create.isPending} error={create.error} onSubmit={(v) => create.mutate(v)} />
          )}
        </section>
      </div>
    </div>
  );
}

function TokenRow({ token: t, revoking, onRevoke }: { token: AccessToken; revoking: boolean; onRevoke: () => void }) {
  return (
    <li className="row">
      <span className="row-main">
        <span className="row-title">
          {t.name} <span className="mono muted">…{t.hint}</span> {t.expired ? <span className="badge">Expired</span> : null}
        </span>
        <span className="row-sub">
          Created {dateFormat.format(new Date(t.createdAt))} · {t.expired ? "expired" : "expires"} {dateFormat.format(new Date(t.expiresAt))} ·{" "}
          {t.lastUsedAt ? `last used ${dateFormat.format(new Date(t.lastUsedAt))}` : "never used"}
        </span>
      </span>
      <span className="row-actions">
        <button type="button" className="button ghost" disabled={revoking} onClick={onRevoke} aria-label={`Revoke ${t.name}`}>
          {revoking ? "Revoking…" : "Revoke"}
        </button>
      </span>
    </li>
  );
}

export function TokenForm({ pending, error, onSubmit }: { pending: boolean; error: unknown; onSubmit: (v: { name: string; expiresInDays: number }) => void }) {
  const [name, setName] = useState("");
  const [days, setDays] = useState(90);
  const lifetimeId = useId();
  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit({ name, expiresInDays: days });
  }
  return (
    <form className="form" onSubmit={submit} noValidate>
      <FormAlert error={error} fields={["name", "expiresInDays"]} />
      <Field
        label="Name"
        name="name"
        required
        maxLength={100}
        value={name}
        onChange={(e) => setName(e.target.value)}
        hint="Where you'll use it, such as GitHub Actions or my laptop."
        error={fieldError(error, "name")}
      />
      <div className="field">
        <label htmlFor={lifetimeId}>Expires after</label>
        <select id={lifetimeId} value={days} onChange={(e) => setDays(Number(e.target.value))}>
          {lifetimes.map((l) => (
            <option key={l.days} value={l.days}>
              {l.label}
            </option>
          ))}
        </select>
      </div>
      <button type="submit" className="button primary" disabled={pending}>
        {pending ? "Creating…" : "Create token"}
      </button>
    </form>
  );
}

/**
 * Shows a new token once, ready to copy. Signalbox keeps only its hash, so
 * it can't be shown again.
 */
export function NewToken({ token, onDone }: { token: NewAccessToken; onDone: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [copied, setCopied] = useState(false);
  const id = useId();
  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, []);
  async function copy() {
    try {
      await navigator.clipboard.writeText(token.token);
      setCopied(true);
    } catch {
      input.current?.select();
    }
  }
  return (
    <div className="invitation-link" role="status">
      <p>
        Copy <strong>{token.name}</strong> now: you won't see it again. It works until {dateFormat.format(new Date(token.expiresAt))}. Send it as{" "}
        <code>Authorization: Bearer …</code>, or set it as <code>SIGNALBOX_TOKEN</code> for the CLI.
      </p>
      <label htmlFor={id} className="field-label">
        Token
      </label>
      <div className="copy-row">
        <input id={id} ref={input} className="mono" readOnly value={token.token} onFocus={(e) => e.target.select()} />
        <button type="button" className="button secondary" onClick={() => void copy()}>
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <button type="button" className="button ghost" onClick={onDone}>
        Done
      </button>
    </div>
  );
}
