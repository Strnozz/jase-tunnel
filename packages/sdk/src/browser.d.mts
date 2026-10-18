export declare const TUNNEL_PROTOCOL: 'jase-tunnel-v1';
export declare function detectPlatform(platform?: string, architecture?: string, bitness?: string, userAgent?: string): string | undefined;
export declare function loopbackPermission(): Promise<'granted' | 'denied' | 'prompt' | 'unknown'>;
export declare class TunnelClient {
  constructor(options?: { path?: string; onState?: (state: any) => void });
  platform(): Promise<string | undefined>; session(): Promise<any>; releases(): Promise<any>;
  canAnnounce(): Promise<boolean>; announce(): Promise<any | null>;
  discover(): Promise<any>; connect(): Promise<any>; disconnect(): Promise<void>; chat(model: string, message: string): Promise<any>; confirm(id: string, allow: boolean): Promise<any>;
}
