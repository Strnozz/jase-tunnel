export type ToolContext = { request: Request; signal: AbortSignal; callId: string; sessionId: string; abortSignal?: AbortSignal; toolCallId?: string; messages?: unknown[] };
export type Tool = { name: string; description: string; parameters: Record<string, unknown>; execute(args: any, context: ToolContext): unknown | Promise<unknown>; readOnly?: boolean; authorize?(context: ToolContext, args: any): boolean | Promise<boolean> };
export type ToolBinding = { tool: unknown; options?: { name?: string; description?: string; parameters?: Record<string, unknown>; readOnly?: boolean } };
export type SiteOptions = { name?: string; origin?: string; path?: string; tools?: readonly (Tool | ToolBinding)[] | ((context: {request: Request; signal: AbortSignal}) => readonly (Tool | ToolBinding)[] | Promise<readonly (Tool | ToolBinding)[]>); instructions?: string; authorize?(request: Request): boolean | Promise<boolean>; onError?(error: unknown): void };
export declare const TUNNEL_PROTOCOL: 'jase-tunnel-v1';
export declare function defineTool<T>(tool: T): T;
export declare function adaptTool(tool: unknown, options?: ToolBinding['options']): Tool;
export declare class TunnelSite { constructor(options?: SiteOptions); manifest(): Response; handle(request: Request): Promise<Response>; }
export declare function defineSite(options?: SiteOptions): TunnelSite;
export declare function fetchHandler(site: TunnelSite): (request: Request) => Promise<Response>;
