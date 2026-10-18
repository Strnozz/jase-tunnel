import type { IncomingMessage, ServerResponse } from 'node:http';
import type { TunnelSite } from './server.mjs';
type Handler = (request: IncomingMessage, response: ServerResponse) => unknown;
export declare function toRequest(request: IncomingMessage, signal?: AbortSignal): Request;
export declare function nodeMiddleware(site: TunnelSite): (request: IncomingMessage, response: ServerResponse, next?: (error?: unknown) => void) => void;
export declare function withJaseTunnel(site: TunnelSite, handler: Handler): Handler;
