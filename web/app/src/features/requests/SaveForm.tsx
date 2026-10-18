// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { type FormEvent, useId, useState } from "react";
import { api, describeError } from "../../api";
import { collectionsQuery, itemsQuery } from "../../queries";
import { folders } from "./model";

export interface SaveTarget {
  collectionId: string;
  parentId: string;
  name: string;
}

/** Where to save a new request: a name, a collection and a folder in it. */
export function SaveForm({
  projectId,
  suggestedName,
  pending,
  error,
  onSave,
  onCancel,
}: {
  projectId: string;
  suggestedName: string;
  pending: boolean;
  error: unknown;
  onSave: (t: SaveTarget) => void;
  onCancel: () => void;
}) {
  const collections = useQuery(collectionsQuery(api, projectId));
  const list = collections.data?.data ?? [];
  const [name, setName] = useState(suggestedName);
  const [collectionId, setCollectionId] = useState("");
  const [parentId, setParentId] = useState("");
  const chosen = collectionId || list[0]?.id || "";
  const items = useQuery({ ...itemsQuery(api, chosen), enabled: chosen !== "" });
  const ids = { name: useId(), collection: useId(), folder: useId() };

  function submit(e: FormEvent) {
    e.preventDefault();
    if (chosen && name.trim()) onSave({ collectionId: chosen, parentId, name: name.trim() });
  }

  if (collections.isSuccess && list.length === 0) {
    return (
      <div className="save-form" role="group" aria-label="Save request">
        <p className="field-hint">Add a collection under Collections first, then save the request into it.</p>
        <button type="button" className="button ghost small" onClick={onCancel}>
          Close
        </button>
      </div>
    );
  }
  return (
    <form className="save-form form" onSubmit={submit} aria-label="Save request">
      <div className="field">
        <label htmlFor={ids.name}>Name</label>
        <input id={ids.name} value={name} onChange={(e) => setName(e.target.value)} maxLength={200} required />
      </div>
      <div className="field">
        <label htmlFor={ids.collection}>Collection</label>
        <select
          id={ids.collection}
          value={chosen}
          onChange={(e) => {
            setCollectionId(e.target.value);
            setParentId("");
          }}
        >
          {list.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label htmlFor={ids.folder}>Folder</label>
        <select id={ids.folder} value={parentId} onChange={(e) => setParentId(e.target.value)}>
          <option value="">The top of the collection</option>
          {folders(items.data?.data ?? []).map(({ item, depth }) => (
            <option key={item.id} value={item.id}>
              {"  ".repeat(depth)}
              {item.name}
            </option>
          ))}
        </select>
      </div>
      {error ? (
        <p className="form-alert" role="alert">
          {describeError(error)}
        </p>
      ) : null}
      <div className="form-actions">
        <button type="submit" className="button primary" disabled={pending || !chosen || !name.trim()}>
          {pending ? "Saving…" : "Save"}
        </button>
        <button type="button" className="button ghost" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
