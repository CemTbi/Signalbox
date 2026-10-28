// SPDX-License-Identifier: Apache-2.0

import type { Collection, Item } from "@signalbox/api-client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { type FormEvent, useId, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { LoadError } from "../../components/Page";
import { collectionsKey, collectionsQuery, itemsKey, itemsQuery } from "../../queries";
import { ImportCollection } from "./ImportCollection";
import { tree } from "./model";

/**
 * A project's collections, each a tree of folders and saved requests.
 * Choosing a request opens it in the editor.
 */
export function CollectionsPanel({
  projectId,
  canEdit,
  openItemId,
  onOpen,
}: {
  projectId: string;
  canEdit: boolean;
  openItemId: string | undefined;
  onOpen: (item: Item) => void;
}) {
  const queryClient = useQueryClient();
  const collections = useQuery(collectionsQuery(api, projectId));
  const [adding, setAdding] = useState(false);
  const [importing, setImporting] = useState(false);
  const create = useMutation({
    mutationFn: (name: string) => unwrap(api.POST("/api/v1/projects/{projectId}/collections", { params: { path: { projectId } }, body: { name } })),
    onSuccess: async () => {
      setAdding(false);
      await queryClient.invalidateQueries({ queryKey: collectionsKey(projectId) });
    },
  });

  return (
    <div className="collections">
      {collections.isError ? <LoadError message={describeError(collections.error)} onRetry={() => void collections.refetch()} /> : null}
      {collections.data && collections.data.data.length === 0 && !adding ? (
        <p className="field-hint">Save requests in collections to find them again and share them with the project.</p>
      ) : null}
      <ul className="tree" role="tree" aria-label="Collections">
        {collections.data?.data.map((c) => (
          <CollectionNode key={c.id} collection={c} projectId={projectId} canEdit={canEdit} openItemId={openItemId} onOpen={onOpen} />
        ))}
      </ul>
      {canEdit ? (
        adding ? (
          <NameForm label="Collection name" submitLabel="Add collection" pending={create.isPending} error={create.error} onSubmit={(n) => create.mutate(n)} onCancel={() => setAdding(false)} />
        ) : importing ? (
          <ImportCollection projectId={projectId} onDone={() => setImporting(false)} />
        ) : (
          <div className="row-actions">
            <button type="button" className="button secondary small" onClick={() => setAdding(true)}>
              New collection
            </button>
            <button type="button" className="button ghost small" onClick={() => setImporting(true)}>
              Import from Postman
            </button>
          </div>
        )
      ) : null}
    </div>
  );
}

function CollectionNode({
  collection: c,
  projectId,
  canEdit,
  openItemId,
  onOpen,
}: {
  collection: Collection;
  projectId: string;
  canEdit: boolean;
  openItemId: string | undefined;
  onOpen: (item: Item) => void;
}) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [adding, setAdding] = useState<string | null>(null); // parent folder ID, or "" for the top
  const items = useQuery({ ...itemsQuery(api, c.id), enabled: open });
  const children = tree(items.data?.data ?? []);
  const addFolder = useMutation({
    mutationFn: ({ name, parentId }: { name: string; parentId: string }) =>
      unwrap(
        api.POST("/api/v1/collections/{collectionId}/items", {
          params: { path: { collectionId: c.id } },
          body: { kind: "folder", name, ...(parentId ? { parentId } : {}) },
        }),
      ),
    onSuccess: async () => {
      setAdding(null);
      await queryClient.invalidateQueries({ queryKey: itemsKey(c.id) });
    },
  });
  const remove = useMutation({
    mutationFn: () => unwrap(api.DELETE("/api/v1/collections/{collectionId}", { params: { path: { collectionId: c.id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: collectionsKey(projectId) }),
  });

  return (
    <li role="treeitem" aria-expanded={open} aria-selected={false}>
      <div className="tree-row">
        <button type="button" className="tree-toggle" onClick={() => setOpen(!open)} aria-label={`${open ? "Close" : "Open"} ${c.name}`}>
          <span aria-hidden="true">{open ? "▾" : "▸"}</span> <strong>{c.name}</strong>
        </button>
        {open ? (
          <span className="tree-actions">
            <Link className="button ghost small" to="/collections/$collectionId/runs" params={{ collectionId: c.id }} aria-label={canEdit ? `Run ${c.name}` : `Runs of ${c.name}`}>
              {canEdit ? "Run" : "Runs"}
            </Link>
            <a className="button ghost small" href={`/api/v1/collections/${c.id}/export`} download aria-label={`Export ${c.name} for Postman`}>
              Export
            </a>
            {canEdit ? (
              <>
                <button type="button" className="button ghost small" onClick={() => setAdding("")}>
                  New folder
                </button>
                <button
                  type="button"
                  className="button ghost small"
                  disabled={remove.isPending}
                  onClick={() => {
                    if (window.confirm(`Delete ${c.name} and everything in it?`)) remove.mutate();
                  }}
                >
                  Delete
                </button>
              </>
            ) : null}
          </span>
        ) : null}
      </div>
      {remove.isError ? (
        <p className="form-alert" role="alert">
          {describeError(remove.error)}
        </p>
      ) : null}
      {open ? (
        <>
          {items.isError ? <LoadError message={describeError(items.error)} onRetry={() => void items.refetch()} /> : null}
          {adding === "" ? (
            <NameForm label="Folder name" submitLabel="Add folder" pending={addFolder.isPending} error={addFolder.error} onSubmit={(name) => addFolder.mutate({ name, parentId: "" })} onCancel={() => setAdding(null)} />
          ) : null}
          {items.isSuccess && (children.get("") ?? []).length === 0 ? <p className="field-hint">Empty. Save a request into it from the editor.</p> : null}
          <Branch
            parent=""
            childrenOf={children}
            collectionId={c.id}
            canEdit={canEdit}
            openItemId={openItemId}
            onOpen={onOpen}
            adding={adding}
            onAddFolder={setAdding}
            addFolder={(name, parentId) => addFolder.mutate({ name, parentId })}
            addPending={addFolder.isPending}
            addError={addFolder.error}
          />
        </>
      ) : null}
    </li>
  );
}

function Branch(props: {
  parent: string;
  childrenOf: Map<string, Item[]>;
  collectionId: string;
  canEdit: boolean;
  openItemId: string | undefined;
  onOpen: (item: Item) => void;
  adding: string | null;
  onAddFolder: (parentId: string | null) => void;
  addFolder: (name: string, parentId: string) => void;
  addPending: boolean;
  addError: unknown;
}) {
  const list = props.childrenOf.get(props.parent) ?? [];
  if (list.length === 0) return null;
  return (
    <ul role="group" className="tree">
      {list.map((it) => (
        <ItemNode key={it.id} item={it} {...props} />
      ))}
    </ul>
  );
}

function ItemNode({ item: it, ...props }: Parameters<typeof Branch>[0] & { item: Item }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(true);
  const [renaming, setRenaming] = useState(false);
  const rename = useMutation({
    mutationFn: (name: string) =>
      unwrap(api.PATCH("/api/v1/items/{itemId}", { params: { path: { itemId: it.id } }, body: { name } })),
    onSuccess: async () => {
      setRenaming(false);
      await queryClient.invalidateQueries({ queryKey: itemsKey(props.collectionId) });
    },
  });
  const remove = useMutation({
    mutationFn: () => unwrap(api.DELETE("/api/v1/items/{itemId}", { params: { path: { itemId: it.id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: itemsKey(props.collectionId) }),
  });
  const actions = props.canEdit ? (
    <span className="tree-actions">
      {it.kind === "folder" ? (
        <>
          <Link className="button ghost small" to="/collections/$collectionId/runs" params={{ collectionId: props.collectionId }} search={{ folder: it.id }} aria-label={`Run ${it.name}`}>
            Run
          </Link>
          <button type="button" className="button ghost small" onClick={() => props.onAddFolder(it.id)}>
            New folder
          </button>
        </>
      ) : null}
      <button type="button" className="button ghost small" onClick={() => setRenaming(true)} aria-label={`Rename ${it.name}`}>
        Rename
      </button>
      <button
        type="button"
        className="button ghost small"
        aria-label={`Delete ${it.name}`}
        disabled={remove.isPending}
        onClick={() => {
          if (window.confirm(it.kind === "folder" ? `Delete ${it.name} and everything in it?` : `Delete ${it.name}?`)) remove.mutate();
        }}
      >
        Delete
      </button>
    </span>
  ) : null;

  return (
    <li role="treeitem" aria-expanded={it.kind === "folder" ? open : undefined} aria-selected={props.openItemId === it.id}>
      {renaming ? (
        <NameForm label={`New name for ${it.name}`} initial={it.name} submitLabel="Rename" pending={rename.isPending} error={rename.error} onSubmit={(n) => rename.mutate(n)} onCancel={() => setRenaming(false)} />
      ) : (
        <div className="tree-row">
          {it.kind === "folder" ? (
            <button type="button" className="tree-toggle" onClick={() => setOpen(!open)}>
              <span aria-hidden="true">{open ? "▾" : "▸"}</span> {it.name}
            </button>
          ) : (
            <button type="button" className="tree-toggle tree-request" onClick={() => props.onOpen(it)} aria-current={props.openItemId === it.id ? "true" : undefined}>
              <span className={`method method-${(it.request?.method ?? "get").toLowerCase()}`}>{it.request?.method}</span> {it.name}
            </button>
          )}
          {actions}
        </div>
      )}
      {remove.isError ? (
        <p className="form-alert" role="alert">
          {describeError(remove.error)}
        </p>
      ) : null}
      {it.kind === "folder" && props.adding === it.id ? (
        <NameForm label="Folder name" submitLabel="Add folder" pending={props.addPending} error={props.addError} onSubmit={(n) => props.addFolder(n, it.id)} onCancel={() => props.onAddFolder(null)} />
      ) : null}
      {it.kind === "folder" && open ? <Branch {...props} parent={it.id} /> : null}
    </li>
  );
}

/** A one-field form for naming a collection, folder or request. */
export function NameForm({
  label,
  initial = "",
  submitLabel,
  pending,
  error,
  onSubmit,
  onCancel,
}: {
  label: string;
  initial?: string;
  submitLabel: string;
  pending: boolean;
  error: unknown;
  onSubmit: (name: string) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial);
  const inputId = useId();
  function submit(e: FormEvent) {
    e.preventDefault();
    if (name.trim()) onSubmit(name.trim());
  }
  return (
    <form className="name-form" onSubmit={submit}>
      <label htmlFor={inputId} className="visually-hidden">
        {label}
      </label>
      <input id={inputId} value={name} onChange={(e) => setName(e.target.value)} placeholder={label} autoFocus maxLength={200} />
      <button type="submit" className="button primary small" disabled={pending || !name.trim()}>
        {submitLabel}
      </button>
      <button type="button" className="button ghost small" onClick={onCancel}>
        Cancel
      </button>
      {error ? (
        <p className="form-alert" role="alert">
          {describeError(error)}
        </p>
      ) : null}
    </form>
  );
}
