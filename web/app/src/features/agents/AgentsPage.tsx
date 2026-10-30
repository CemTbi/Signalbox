// SPDX-License-Identifier: Apache-2.0

import type { Agent } from "@signalbox/api-client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { api, describeError, unwrap } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { agentsKey, agentsQuery } from "../../queries";

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

/**
 * Your agents: `signalbox agent` processes that send your requests from
 * your own machines, and how to start one.
 */
export function AgentsPage() {
  const queryClient = useQueryClient();
  const agents = useQuery(agentsQuery(api));
  const remove = useMutation({
    mutationFn: (agentId: string) => unwrap(api.DELETE("/api/v1/agents/{agentId}", { params: { path: { agentId } } })),
    onSettled: () => queryClient.invalidateQueries({ queryKey: agentsKey }),
  });

  return (
    <div className="stack">
      <PageHeader
        title="Agents"
        meta={
          <p className="lede">
            An agent sends your requests from your own machine, so <code>localhost</code>, private networks and your VPN work. It sends only your requests, and opens
            no port.
          </p>
        }
      />
      <div className="split">
        <section aria-labelledby="agents" className="split-main">
          <h2 id="agents" className="section-title">
            Your agents
          </h2>
          {agents.isPending ? <Loading label="Loading agents…" /> : null}
          {agents.isError ? <LoadError message={describeError(agents.error)} onRetry={() => void agents.refetch()} /> : null}
          {remove.isError ? (
            <p className="form-alert" role="alert">
              The agent couldn't be deleted: {describeError(remove.error)}
            </p>
          ) : null}
          {agents.data && agents.data.data.length === 0 ? (
            <div className="empty-state">
              <p className="empty-title">No agents yet</p>
              <p>Start one on your machine, and it appears here.</p>
            </div>
          ) : null}
          {agents.data && agents.data.data.length > 0 ? (
            <ul className="rows">
              {agents.data.data.map((a) => (
                <AgentRow key={a.id} agent={a} deleting={remove.isPending && remove.variables === a.id} onDelete={() => remove.mutate(a.id)} />
              ))}
            </ul>
          ) : null}
        </section>
        <section aria-labelledby="start-agent" className="panel split-side">
          <h2 id="start-agent" className="section-title">
            Start an agent
          </h2>
          <ol className="agent-steps">
            <li>
              Create an <Link to="/account/tokens">access token</Link> for it.
            </li>
            <li>
              On your machine, with the Signalbox CLI, run:
              <pre className="code">
                <code>{`SIGNALBOX_TOKEN=sbx_pat_… signalbox agent --server ${window.location.origin}`}</code>
              </pre>
            </li>
            <li>Choose it under Run on, in Requests or when you run a collection.</li>
          </ol>
          <p className="field-hint">
            It takes the machine's name; <code>--name</code> picks another. <code>--deny 10.0.0.0/8</code> keeps requests from an address range.
          </p>
        </section>
      </div>
    </div>
  );
}

function AgentRow({ agent: a, deleting, onDelete }: { agent: Agent; deleting: boolean; onDelete: () => void }) {
  const details = [a.hostname, a.os, a.version ? `version ${a.version}` : ""].filter(Boolean).join(" · ");
  return (
    <li className="row">
      <span className="row-main">
        <span className="row-title">
          {a.name} <span className={a.online ? "badge online" : "badge"}>{a.online ? "Online" : "Offline"}</span>
        </span>
        <span className="row-sub">
          {details ? `${details} · ` : ""}
          {a.online ? "ready for your requests" : `last seen ${timeFormat.format(new Date(a.lastSeenAt))}`}
        </span>
      </span>
      <span className="row-actions">
        <button type="button" className="button ghost" disabled={deleting} onClick={onDelete} aria-label={`Delete ${a.name}`}>
          {deleting ? "Deleting…" : "Delete"}
        </button>
      </span>
    </li>
  );
}
