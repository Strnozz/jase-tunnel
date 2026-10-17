import { createHash, randomBytes } from "node:crypto";
import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { COMPANION_PROTOCOL, type CompanionModel, type PersonalModel, type PersonalModelChoice } from "./protocol";
import type { AgentMessage, ModelTurn, ToolDefinition } from "./types";
import { nextTrace, type CompanionTrace } from "./trace";

const hash = (value: string) => createHash("sha256").update(value).digest("hex");
const secret = () => randomBytes(32).toString("hex");
const AGE = 30 * 86400_000, ONLINE = 90_000, MAX_CONNECTIONS = 10_000, HOUR = 3600_000;
const envInt = (key: string, fallback: number, min: number, max: number) => { const n = Number(process.env[key] ?? fallback); return Number.isSafeInteger(n) && n >= min && n <= max ? n : fallback; };
// Requests waiting for one computer (it answers one at a time) and model turns per browser
// per hour. A turn carries at most 6 tool calls, so the hourly budget also bounds how much
// server work (searches, page reads) one anonymous browser can drive.
const QUEUE_PER_DEVICE = () => envInt("JASE_COMPANION_QUEUE", 8, 1, 50);
const TURNS_PER_HOUR = () => envInt("JASE_COMPANION_TURNS_PER_HOUR", 240, 10, 10_000);
export class CompanionError extends Error {
  constructor(readonly status: number, readonly code: string, message: string) { super(message); }
}
type Connection = { id: string; browserHash: string; tokenHash: string; label: string; models: CompanionModel[]; lastSeen: number; expiresAt: number };
type Ticket = { browserHash: string; expiresAt: number };
export type InferenceTask = Readonly<{
  id: string; model: CompanionModel; messages: readonly AgentMessage[]; tools: readonly ToolDefinition[];
  schema?: Readonly<Record<string, unknown>>; temperature: number; maxTokens: number; contextTokens: number; expiresAt: number;
  // For the desktop app's log only (trace.ts).
  trace?: CompanionTrace;
}>;
type Pending = { connection: string; task: InferenceTask; delivered: boolean; heartbeat: number; finish: (turn?: ModelTurn, error?: Error) => void; deliver: () => InferenceTask };

// One broker per Next process, shared across route bundles. Device metadata is durable;
// prompts/tasks exist in memory only and are never written to the device registry.
export class CompanionBroker {
  private readonly debugId = randomBytes(4).toString("hex");
  private connections = new Map<string, Connection>();
  private tickets = new Map<string, Ticket>();
  private pending = new Map<string, Pending>();
  private wake = new Map<string, () => void>();
  // Model turns per browser in the current hour (by browser, so re-pairing does not reset it).
  private usage = new Map<string, { start: number; count: number }>();
  // Which browser owns a personal Goal. Bound to the browser, not to the computer's
  // connection, so re-pairing the app or a new connection keeps the person's Goals.
  private owners = new Map<string, string>();
  constructor(private readonly path?: string, private readonly now = Date.now) {
    this.debug("start");
    if (!path) return;
    try {
      const saved = JSON.parse(readFileSync(path, "utf8"));
      if (saved.protocol !== COMPANION_PROTOCOL || !Array.isArray(saved.connections)) throw new Error("Invalid companion registry");
      for (const c of saved.connections) if (typeof c.id === "string" && /^[a-f0-9]{32}$/.test(c.id)
        && /^[a-f0-9]{64}$/.test(c.browserHash) && /^[a-f0-9]{64}$/.test(c.tokenHash) && c.expiresAt > now()) {
        this.connections.set(c.id, { ...c, models: validateModels(c.models), lastSeen: 0 });
      }
    } catch (error) { if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error; }
  }
  private debug(event: string) {
    if (process.env.JASE_COMPANION_DEBUG === "true") console.info("Jase companion", { pid: process.pid, broker: this.debugId, event, pending: this.pending.size });
  }
  private save() {
    if (!this.path) return;
    mkdirSync(dirname(this.path), { recursive: true, mode: 0o700 });
    const temporary = `${this.path}.${process.pid}.tmp`;
    writeFileSync(temporary, JSON.stringify({ protocol: COMPANION_PROTOCOL, connections: [...this.connections.values()] }), { mode: 0o600 });
    renameSync(temporary, this.path);
  }
  ticket(browser: string): string {
    for (const [key, value] of this.tickets) if (value.expiresAt <= this.now() || value.browserHash === hash(browser)) this.tickets.delete(key);
    if (this.tickets.size >= 2000) throw new CompanionError(503, "busy", "Riprova tra poco.");
    const value = secret(); this.tickets.set(hash(value), { browserHash: hash(browser), expiresAt: this.now() + 5 * 60_000 }); return value;
  }
  pair(ticket: string, label: string, models: unknown) {
    const entry = this.tickets.get(hash(ticket));
    if (!entry || entry.expiresAt <= this.now()) throw new CompanionError(403, "pairing_expired", "Collegamento scaduto: riprova dal sito Jase.");
    const checked = validateModels(models);
    if (typeof label !== "string" || label.length < 1 || label.length > 80 || /[\x00-\x1f]/.test(label)) throw new CompanionError(400, "invalid_device", "Nome del dispositivo non valido.");
    for (const c of [...this.connections.values()]) if (c.expiresAt <= this.now() || c.browserHash === entry.browserHash) this.remove(c.id);
    if (this.connections.size >= MAX_CONNECTIONS) throw new CompanionError(503, "busy", "Riprova tra poco.");
    this.tickets.delete(hash(ticket));
    const token = secret(), id = randomBytes(16).toString("hex");
    this.connections.set(id, { id, browserHash: entry.browserHash, tokenHash: hash(token), label, models: checked, lastSeen: this.now(), expiresAt: this.now() + AGE });
    this.save(); return { protocol: COMPANION_PROTOCOL, connectionId: id, token };
  }
  authenticate(token: string): Connection {
    if (!/^[a-f0-9]{64}$/.test(token)) throw new CompanionError(401, "unauthorized", "Dispositivo non collegato.");
    const fingerprint = hash(token);
    const c = [...this.connections.values()].find(value => value.tokenHash === fingerprint && value.expiresAt > this.now());
    if (!c) throw new CompanionError(401, "unauthorized", "Dispositivo non collegato.");
    c.lastSeen = this.now();
    // Sliding expiry: a computer in use stays connected. Written at most once a day.
    if (c.expiresAt - this.now() < AGE - 86400_000) { c.expiresAt = this.now() + AGE; this.save(); }
    return c;
  }
  // Goal ownership (see owners). Stored one file per Goal next to the device registry.
  claimGoal(goal: string, browser: string) {
    if (!/^[0-9a-f-]{36}$/i.test(goal)) throw new Error("Invalid Goal reference.");
    this.owners.set(goal, hash(browser));
    if (this.owners.size > 50_000) this.owners.delete(this.owners.keys().next().value!);
    if (!this.path) return;
    const directory = join(dirname(this.path), "goals"); mkdirSync(directory, { recursive: true, mode: 0o700 });
    writeFileSync(join(directory, goal), hash(browser), { mode: 0o600 });
  }
  goalOwner(goal: string): string | undefined {
    if (!/^[0-9a-f-]{36}$/i.test(goal)) return;
    const known = this.owners.get(goal); if (known || !this.path) return known;
    try { const value = readFileSync(join(dirname(this.path), "goals", goal), "utf8").trim(); return /^[a-f0-9]{64}$/.test(value) ? value : undefined; }
    catch (error) { if ((error as NodeJS.ErrnoException).code === "ENOENT") return; throw error; }
  }
  ownsGoal(browser: string | undefined, goal: string, choice: PersonalModelChoice): boolean {
    const owner = this.goalOwner(goal);
    // Goals created before ownership was recorded: the browser must still hold the model.
    return owner ? !!browser && hash(browser) === owner : this.owns(browser, choice);
  }
  // The same model on the owner's CURRENT connection: after re-pairing the connection id
  // changes, the model key does not. Unchanged when nothing better is known.
  current(choice: PersonalModelChoice, owner: string | undefined): PersonalModelChoice {
    const [connection, model] = choice.slice("personal:".length).split(".");
    if (this.connections.has(connection) || !owner) return choice;
    for (const c of this.connections.values()) {
      if (c.browserHash !== owner || c.expiresAt <= this.now()) continue;
      const found = c.models.find(m => hash(m.key).slice(0, 24) === model);
      if (found) return this.choice(c, found);
    }
    return choice;
  }
  models(browser: string | undefined): PersonalModel[] {
    if (!browser) return [];
    return [...this.connections.values()].filter(c => c.browserHash === hash(browser) && c.expiresAt > this.now()).flatMap(c => c.models.map(model => ({
      ...model, id: this.choice(c, model), label: model.name, provider: "PersonalProvider" as const,
      available: c.lastSeen > this.now() - ONLINE && model.tools
    })));
  }
  browserState(browser: string | undefined) {
    const c = browser ? [...this.connections.values()].find(c => c.browserHash === hash(browser) && c.expiresAt > this.now()) : undefined;
    return { paired: !!c, connected: !!c && c.lastSeen > this.now() - ONLINE, ...(c ? { label: c.label, connectionId: c.id } : {}) };
  }
  owns(browser: string | undefined, choice: PersonalModelChoice): boolean { return this.models(browser).some(m => m.id === choice); }
  private choice(c: Connection, model: CompanionModel): PersonalModelChoice { return `personal:${c.id}.${hash(model.key).slice(0, 24)}`; }
  private locate(choice: PersonalModelChoice) {
    const c = this.connections.get(choice.split(":")[1]?.split(".")[0]);
    const model = c?.models.find(m => this.choice(c, m) === choice);
    if (!c || !model || c.expiresAt <= this.now() || c.lastSeen <= this.now() - ONLINE) throw new CompanionError(503, "device_offline", "Il tuo computer non è collegato. Apri l’app Jase e riprova.");
    return { c, model };
  }
  modelContext(choice: PersonalModelChoice) { return this.locate(choice).model.contextTokens ?? 32768; }
  // timeoutMs: the computer's own time for one answer, counted from delivery; queueMs: how
  // long a request may wait behind the ones before it.
  async infer(choice: PersonalModelChoice, input: Omit<InferenceTask, "id" | "model" | "expiresAt" | "trace">, signal?: AbortSignal, timeoutMs = 600_000, queueMs = 900_000): Promise<ModelTurn> {
    const { c, model } = this.locate(choice);
    if (input.tools.length && !model.tools) throw new CompanionError(400, "unsupported_tools", "Questo modello non supporta gli strumenti di Jase.");
    if (signal?.aborted) throw signal.reason;
    if (JSON.stringify(input).length > 1_000_000) throw new CompanionError(413, "context_too_large", "Il contesto è troppo grande per il modello personale.");
    // Queued, not refused: the computer answers in order. Refused only beyond the queue.
    if (this.pending.size >= 500 || [...this.pending.values()].filter(p => p.connection === c.id).length >= QUEUE_PER_DEVICE()) throw new CompanionError(429, "model_busy", "Il tuo modello ha già troppe richieste in coda. Attendi e riprova.");
    const used = this.usage.get(c.browserHash), now = this.now();
    if (used && now - used.start < HOUR && used.count >= TURNS_PER_HOUR()) throw new CompanionError(429, "hourly_limit", "Hai raggiunto il limite orario di richieste con il tuo modello. Riprova più tardi.");
    if (!used || now - used.start >= HOUR) { if (this.usage.size > 20_000) this.usage.clear(); this.usage.set(c.browserHash, { start: now, count: 1 }); } else used.count++;
    const trace = nextTrace();
    const id = secret(), task = { ...input, id, model, contextTokens: Math.min(input.contextTokens, model.contextTokens ?? input.contextTokens), expiresAt: this.now() + timeoutMs, ...(trace ? { trace } : {}) };
    return new Promise<ModelTurn>((resolve, reject) => {
      const abort = () => finish(undefined, signal?.reason ?? new Error("Model request cancelled"));
      const late = () => finish(undefined, new CompanionError(504, "model_timeout", "Il modello personale non ha risposto in tempo."));
      let timer = setTimeout(late, queueMs); timer.unref();
      const deliver = (): InferenceTask => {
        clearTimeout(timer); timer = setTimeout(late, timeoutMs); timer.unref();
        return { ...task, expiresAt: this.now() + timeoutMs };
      };
      const watch = setInterval(() => {
        const p = this.pending.get(id);
        if (p && (c.lastSeen <= this.now() - ONLINE || p.delivered && p.heartbeat <= this.now() - 30_000)) p.finish(undefined, new CompanionError(503, "device_offline", "Il collegamento al tuo computer si è interrotto. Apri l’app Jase e riprova."));
      }, 10_000); watch.unref();
      const finish = (turn?: ModelTurn, error?: Error) => {
        if (!this.pending.delete(id)) return;
        clearTimeout(timer); clearInterval(watch); signal?.removeEventListener("abort", abort);
        error ? reject(error) : resolve(turn!);
      };
      this.pending.set(id, { connection: c.id, task, delivered: false, heartbeat: this.now(), finish, deliver });
      this.debug("queued");
      signal?.addEventListener("abort", abort, { once: true });
      this.wake.get(c.id)?.();
    });
  }
  async poll(token: string, signal?: AbortSignal, waitMs = 20_000): Promise<InferenceTask | null> {
    const c = this.authenticate(token);
    if (this.wake.has(c.id)) throw new CompanionError(409, "already_polling", "Collegamento già attivo.");
    const next = () => [...this.pending.values()].find(p => p.connection === c.id && !p.delivered);
    if (!next() && !signal?.aborted) await new Promise<void>(resolve => {
      const done = () => { clearTimeout(timer); signal?.removeEventListener("abort", done); this.wake.delete(c.id); resolve(); };
      const timer = setTimeout(done, waitMs); timer.unref();
      this.wake.set(c.id, done); signal?.addEventListener("abort", done, { once: true });
    });
    const pending = next(); this.debug(signal?.aborted ? "poll-aborted" : pending ? "delivered" : "poll-empty"); if (!pending || signal?.aborted) return null;
    pending.delivered = true; pending.heartbeat = this.now(); return pending.deliver();
  }
  active(token: string, id: string) {
    const c = this.authenticate(token), pending = this.pending.get(id);
    if (!pending || pending.connection !== c.id) return false;
    if (pending.delivered && pending.heartbeat <= this.now() - 30_000) {
      pending.finish(undefined, new CompanionError(503, "device_offline", "Il collegamento al tuo computer si è interrotto. Riprova.")); return false;
    }
    pending.heartbeat = this.now(); return true;
  }
  result(token: string, id: string, value: unknown) {
    const c = this.authenticate(token), p = this.pending.get(id);
    if (!p || p.connection !== c.id || !p.delivered) throw new CompanionError(404, "request_expired", "Richiesta scaduta.");
    const v = value as Record<string, unknown>;
    if (v?.error) { p.finish(undefined, new CompanionError(503, "local_model_error", "Il modello sul tuo computer non ha completato la richiesta.")); return; }
    const turn = validateTurn(value); p.finish(turn); this.debug("answered");
  }
  update(token: string, models: unknown) {
    const c = this.authenticate(token), checked = validateModels(models);
    // Heartbeats need no disk write: lastSeen is deliberately reset after restart.
    if (JSON.stringify(c.models) === JSON.stringify(checked)) return;
    c.models = checked; this.save();
  }
  disconnect(browser: string | undefined) { if (!browser) return; for (const c of [...this.connections.values()]) if (c.browserHash === hash(browser)) this.remove(c.id); this.save(); }
  private remove(id: string) { this.connections.delete(id); this.wake.get(id)?.(); for (const p of [...this.pending.values()]) if (p.connection === id) p.finish(undefined, new CompanionError(503, "device_disconnected", "Computer scollegato.")); }
}

export function validateModels(input: unknown): CompanionModel[] {
  if (!Array.isArray(input) || input.length > 100) throw new CompanionError(400, "invalid_models", "Elenco dei modelli non valido.");
  const keys = new Set<string>();
  return input.map(value => {
    const m = value as CompanionModel;
    if (!m || typeof m.key !== "string" || m.key.length > 300 || !/^(ollama|lmstudio):[^\x00-\x1f]+$/.test(m.key)
      || typeof m.name !== "string" || !m.name.length || m.name.length > 200 || /[\x00-\x1f]/.test(m.name)
      || !["ollama", "lmstudio"].includes(m.engine) || !m.key.startsWith(`${m.engine}:`) || typeof m.tools !== "boolean" || keys.has(m.key)) throw new CompanionError(400, "invalid_models", "Modello non valido.");
    keys.add(m.key);
    return { key: m.key, name: m.name, engine: m.engine, tools: m.tools,
      ...(Number.isSafeInteger(m.sizeBytes) && m.sizeBytes! >= 0 ? { sizeBytes: m.sizeBytes } : {}),
      ...(Number.isSafeInteger(m.contextTokens) && m.contextTokens! >= 2048 && m.contextTokens! <= 262144 ? { contextTokens: m.contextTokens } : {}) };
  });
}
export function validateTurn(input: unknown): ModelTurn {
  const value = input as ModelTurn;
  if (!value || typeof value.content !== "string" || value.content.length > 64_000 || !Array.isArray(value.toolCalls) || value.toolCalls.length > 6
    || value.toolCalls.some(c => !c || !/^[a-z][a-z0-9_]{1,40}$/.test(c.name) || !c.arguments || typeof c.arguments !== "object" || Array.isArray(c.arguments))
    || JSON.stringify(value).length > 128_000 || (!value.content.trim() && !value.toolCalls.length)) throw new CompanionError(400, "invalid_result", "Risposta del modello non valida.");
  return { content: value.content, toolCalls: value.toolCalls.map(c => ({ name: c.name, arguments: c.arguments })) };
}
const globalBroker = globalThis as typeof globalThis & { jaseCompanionBroker?: CompanionBroker };
export const companionBroker = () => globalBroker.jaseCompanionBroker ??= new CompanionBroker(join(process.env.JASE_COMPANION_STATE_DIR ?? join(process.cwd(), "data", "companion"), "devices.json"));
