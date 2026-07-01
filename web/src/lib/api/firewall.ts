import { api } from '$lib/api/client';

export interface FirewallStatus {
  type: 'ufw' | 'iptables' | 'firewalld' | 'none' | string;
  active: boolean;
  default: string;
}

export interface FirewallRule {
  id: string;
  action: 'ALLOW' | 'DENY' | 'REJECT' | 'DROP' | string;
  protocol: 'tcp' | 'udp' | 'both' | string;
  port: string;
  source: string;
  direction: 'IN' | 'OUT' | 'FORWARD' | string;
}

export interface FirewallRules {
  status: FirewallStatus;
  rules: FirewallRule[];
  ipv6?: FirewallRule[];
}

export interface FirewallRuleRequest {
  action: 'ALLOW' | 'DENY' | 'REJECT' | 'DROP' | string;
  protocol: 'tcp' | 'udp' | 'both' | string;
  port: string;
  source: string;
  direction: 'IN' | 'OUT' | 'FORWARD' | string;
}

export const firewallApi = {
  get: (serverID: number) => api.get(`/servers/${serverID}/firewall`) as Promise<FirewallRules>,
  addRule: (serverID: number, req: FirewallRuleRequest) =>
    api.post<void>(`/servers/${serverID}/firewall/rules`, req),
  deleteRule: (serverID: number, ruleID: string) =>
    api.delete<void>(`/servers/${serverID}/firewall/rules/${encodeURIComponent(ruleID)}`),
  enable: (serverID: number) => api.post<void>(`/servers/${serverID}/firewall/enable`),
  disable: (serverID: number) => api.post<void>(`/servers/${serverID}/firewall/disable`),
};
