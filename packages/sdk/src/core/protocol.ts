// Public protocol only. No device credentials or local file paths cross this boundary.
export const TUNNEL_PROTOCOL = "jase-tunnel-v1";

export type PersonalModelChoice = `personal:${string}`;
export const isPersonalModel = (value: unknown): value is PersonalModelChoice =>
  typeof value === "string" && /^personal:[a-f0-9]{32}\.[a-f0-9]{24}$/.test(value);
export type TunnelModel = Readonly<{
  key: string; name: string; engine: "ollama" | "lmstudio"; sizeBytes?: number;
  contextTokens?: number; tools: boolean;
}>;
export type PersonalModel = TunnelModel & Readonly<{ id: PersonalModelChoice; label: string; available: boolean }>;
