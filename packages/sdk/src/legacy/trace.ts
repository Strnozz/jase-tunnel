import { AsyncLocalStorage } from "node:async_hooks";
import { randomBytes } from "node:crypto";

// What a personal model's request belongs to, shown in the desktop app's log ("Start query
// · Goal 1a2b3c4d · turno 3"): one question of the person can take several model turns.
// Only a short reference, never the request text. Shared across Next route bundles.
export type CompanionTrace = Readonly<{ kind: "goal" | "conversation"; ref: string; turn: number }>;
type Scope = { kind: CompanionTrace["kind"]; ref: string };
const root = globalThis as typeof globalThis & { jaseCompanionTrace?: AsyncLocalStorage<Scope>; jaseCompanionTurns?: Map<string, number> };
const context = root.jaseCompanionTrace ??= new AsyncLocalStorage<Scope>();
// Turns continue across the Goal's steps (each step is a separate run of the runner).
const turns = root.jaseCompanionTurns ??= new Map<string, number>();

export const withGoalTrace = <T>(goal: string, action: () => T): T => context.run({ kind: "goal", ref: goal.slice(0, 8) }, action);
export const withConversationTrace = <T>(action: () => T): T => context.run({ kind: "conversation", ref: randomBytes(4).toString("hex") }, action);
export function nextTrace(): CompanionTrace | undefined {
  const scope = context.getStore(); if (!scope) return;
  const key = `${scope.kind}:${scope.ref}`, turn = (turns.get(key) ?? 0) + 1;
  if (turns.size > 10_000) turns.clear();
  turns.set(key, turn);
  return { kind: scope.kind, ref: scope.ref, turn };
}
