import { useMemo } from "react";
import type { Agent } from "../domain/agents";
import { apiClient } from "../client";
import { type ApiResource, useApiResource } from "./useApiResource";
import { useInvalidateKeys } from "./useInvalidateKeys";

export function useAgent(namespace: string | undefined, name: string | undefined) {
  return useApiResource(
    namespace && name ? ["agents.get", namespace, name] : null,
    () => apiClient.agentBuildingBlocks.agent(namespace!, name!),
  );
}

/**
 * The Agents in every namespace named, from one cluster-wide read.
 *
 * `ListAgents` with no namespace lists every namespace the caller's authorization
 * scope allows, so every caller on screen shares one key and one request whatever
 * namespaces it asks about, and a Refresh sends one read rather than one per
 * namespace. Each caller keeps only the namespaces it named.
 */
export function useAgentsAcrossNamespaces(
  namespaces: readonly string[] | undefined,
): ApiResource<{ agents: Agent[] }> {
  const all = useApiResource(namespaces ? ["agents.list", ""] : null, () =>
    apiClient.agentBuildingBlocks.agents(),
  );
  const key = namespaces ? [...namespaces].sort().join(",") : undefined;
  const data = useMemo(() => {
    if (!all.data || key === undefined) return undefined;
    const named = new Set(key.split(",").filter(Boolean));
    return { agents: all.data.filter((agent) => named.has(agent.namespace)) };
  }, [all.data, key]);
  return { ...all, data, isEmpty: !all.error && data?.agents.length === 0 };
}

const KEYS = ["agents."];

/** Re-reads every Agent list and detail on screen. */
export function useInvalidateAgents(): () => Promise<void> {
  return useInvalidateKeys(KEYS);
}
