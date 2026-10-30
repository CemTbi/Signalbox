// SPDX-License-Identifier: Apache-2.0

import type { Agent } from "@signalbox/api-client";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useEffect, useId, useState } from "react";
import { api } from "../../api";
import { agentsQuery } from "../../queries";

const storageKey = "signalbox.agent";

/** The agent chosen last, on any page: agents are the person's, not a project's. */
export function rememberedAgent(): string {
  try {
    return localStorage.getItem(storageKey) ?? "";
  } catch {
    return "";
  }
}

/** Remembers the agent chosen, or "" for the server. */
export function rememberAgent(agentId: string) {
  try {
    localStorage.setItem(storageKey, agentId);
  } catch {
    // Remembering is a convenience; without storage the choice lasts the visit.
  }
}

/**
 * Where requests go out from: the server, or one of your agents. The
 * choice carries across pages and visits.
 */
export function useRunOn() {
  const agents = useQuery(agentsQuery(api));
  const [agentId, setAgentId] = useState(rememberedAgent);
  const list = agents.data?.data ?? [];
  const agent = list.find((a) => a.id === agentId);
  useEffect(() => {
    // An agent that's been deleted isn't chosen.
    if (agents.isSuccess && agentId !== "" && !agent) setAgentId("");
  }, [agents.isSuccess, agentId, agent]);
  return {
    agent,
    agents: list,
    choose(id: string) {
      setAgentId(id);
      rememberAgent(id);
    },
  };
}

/** Run on: the server or one of your agents. Shown once you have an agent. */
export function RunOnSelect({ agent, agents, onChange }: { agent: Agent | undefined; agents: Agent[]; onChange: (agentId: string) => void }) {
  const id = useId();
  const hint = useId();
  if (agents.length === 0) return null;
  return (
    <>
      <label htmlFor={id}>Run on</label>
      <select id={id} value={agent?.id ?? ""} onChange={(e) => onChange(e.target.value)} aria-describedby={agent && !agent.online ? hint : undefined}>
        <option value="">Server</option>
        {agents.map((a) => (
          <option key={a.id} value={a.id}>
            {a.online ? a.name : `${a.name} (offline)`}
          </option>
        ))}
      </select>
      {agent && !agent.online ? (
        <span id={hint} className="field-hint">
          {agent.name} isn't running. Start <code>signalbox agent</code> there, or see <Link to="/account/agents">Agents</Link>.
        </span>
      ) : null}
    </>
  );
}
