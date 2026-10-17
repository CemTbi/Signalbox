// SPDX-License-Identifier: Apache-2.0

import { micromark } from "micromark";
import { gfm, gfmHtml } from "micromark-extension-gfm";
import { useMemo } from "react";

/**
 * CommonMark with GitHub's tables, task lists and autolinks, as specs write
 * their descriptions. Anyone who can edit a spec writes these, so raw HTML
 * is escaped and links with protocols such as javascript: are dropped;
 * those are micromark's defaults.
 */
export function Markdown({ source, className }: { source: string | undefined; className?: string }) {
  const html = useMemo(
    () =>
      source
        ? micromark(source, { extensions: [gfm()], htmlExtensions: [gfmHtml()] })
            // Code blocks and tables scroll sideways; keyboards must reach them.
            .replaceAll("<pre>", '<pre tabindex="0">')
            .replaceAll("<table>", '<table tabindex="0">')
        : "",
    [source],
  );
  if (!html) return null;
  return <div className={className ? `markdown ${className}` : "markdown"} dangerouslySetInnerHTML={{ __html: html }} />;
}
