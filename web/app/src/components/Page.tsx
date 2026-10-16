// SPDX-License-Identifier: Apache-2.0

import { Link } from "@tanstack/react-router";
import { type ReactNode, useEffect, useRef } from "react";

export interface Crumb {
  label: string;
  to?: string;
  params?: Record<string, string>;
}

/**
 * The top of a page: where it sits, its title and its actions. The title
 * takes focus when the page opens, so screen readers announce the new page
 * after client-side navigation.
 */
export function PageHeader({
  title,
  crumbs,
  meta,
  actions,
  documentTitle,
}: {
  title: string;
  crumbs?: Crumb[];
  meta?: ReactNode;
  actions?: ReactNode;
  documentTitle?: string;
}) {
  const heading = useRef<HTMLHeadingElement>(null);
  useDocumentTitle(documentTitle ?? title);
  useEffect(() => {
    heading.current?.focus({ preventScroll: true });
  }, [title]);

  return (
    <div className="page-header">
      {crumbs && crumbs.length > 0 ? (
        <nav aria-label="Breadcrumb" className="breadcrumb">
          <ol>
            {crumbs.map((c) => (
              <li key={c.label}>
                {/* Paths here are built by the app, not typed by people. */}
                {c.to ? (
                  <Link to={c.to as never} params={c.params as never}>
                    {c.label}
                  </Link>
                ) : (
                  <span aria-current="page">{c.label}</span>
                )}
              </li>
            ))}
          </ol>
        </nav>
      ) : null}
      <div className="page-title-row">
        <div className="page-title">
          <h1 ref={heading} tabIndex={-1}>
            {title}
          </h1>
          {meta ? <div className="page-meta">{meta}</div> : null}
        </div>
        {actions ? <div className="page-actions">{actions}</div> : null}
      </div>
    </div>
  );
}

/** Sets the browser tab's title while the component is shown. */
export function useDocumentTitle(title: string) {
  useEffect(() => {
    document.title = `${title} · Signalbox`;
  }, [title]);
}

/** A placeholder for content that is loading. */
export function Loading({ label }: { label: string }) {
  return (
    <p className="loading" role="status">
      {label}
    </p>
  );
}

/** Shows that something failed to load, with a way to try again. */
export function LoadError({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="load-error" role="alert">
      <p>{message}</p>
      {onRetry ? (
        <button type="button" className="button secondary" onClick={onRetry}>
          Try again
        </button>
      ) : null}
    </div>
  );
}
