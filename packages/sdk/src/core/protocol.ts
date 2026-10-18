// Public protocol only. No device credentials or local file paths cross this boundary.
export const COMPANION_PROTOCOL = "jase-companion-v1";
export const COMPANION_PORT = 11439;

// A server restart briefly marks an existing connection offline. Reuse that
// association; replacing it would invalidate the model pinned to ongoing Goals.
export function companionNeedsPairing(server: { paired: boolean; connectionId?: string }, local: { connectionIds?: unknown }): boolean {
  return !server.paired || Array.isArray(local.connectionIds) && !local.connectionIds.includes(server.connectionId);
}
export type PersonalModelChoice = `personal:${string}`;
export const isPersonalModel = (value: unknown): value is PersonalModelChoice =>
  typeof value === "string" && /^personal:[a-f0-9]{32}\.[a-f0-9]{24}$/.test(value);
export type CompanionModel = Readonly<{
  key: string; name: string; engine: "ollama" | "lmstudio"; sizeBytes?: number;
  contextTokens?: number; tools: boolean;
}>;
export type PersonalModel = CompanionModel & Readonly<{
  id: PersonalModelChoice; label: string; provider: "PersonalProvider"; available: boolean;
}>;
