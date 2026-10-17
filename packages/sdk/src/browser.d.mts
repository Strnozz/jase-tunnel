export declare const TUNNEL_PROTOCOL: 'jase-tunnel-v1';
export declare class TunnelClient {
  constructor(options?: { path?: string; onState?: (state: any) => void });
  platform(): Promise<any>; session(): Promise<any>; releases(): Promise<any>; discover(): Promise<any>; connect(): Promise<any>; disconnect(): Promise<void>; chat(model: string, message: string): Promise<any>; confirm(id: string, allow: boolean): Promise<any>;
}
