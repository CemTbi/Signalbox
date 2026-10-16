// SPDX-License-Identifier: Apache-2.0

import type { NewInvitation } from "@signalbox/api-client";
import { useEffect, useId, useRef, useState } from "react";
import { roleLabels } from "../workspaces/roles";

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });

/**
 * Shows a new invitation's link once, ready to copy. Copying uses the
 * clipboard where the browser allows it, which needs HTTPS or localhost;
 * elsewhere the link is selected so it can be copied by hand.
 */
export function InvitationLink({ invitation, onDone }: { invitation: NewInvitation; onDone: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [copied, setCopied] = useState(false);
  const id = useId();

  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, []);

  async function copy() {
    try {
      await navigator.clipboard.writeText(invitation.url);
      setCopied(true);
    } catch {
      input.current?.select();
    }
  }

  return (
    <div className="invitation-link" role="status">
      <p>
        Send this link to <strong>{invitation.email}</strong>. It makes them {article(invitation.role)}{" "}
        {roleLabels[invitation.role].toLowerCase()}, works once, and expires on{" "}
        {dateFormat.format(new Date(invitation.expiresAt))}. You won't see it again.
      </p>
      <label htmlFor={id} className="field-label">
        Invitation link
      </label>
      <div className="copy-row">
        <input id={id} ref={input} className="mono" readOnly value={invitation.url} onFocus={(e) => e.target.select()} />
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

function article(role: string) {
  return role === "owner" || role === "admin" || role === "editor" ? "an" : "a";
}
