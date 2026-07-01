export interface CPUMetrics {
  usage: number;
  cores: number;
  model: string;
}

export interface MemoryMetrics {
  total: number;
  used: number;
  free: number;
  available: number;
  cached: number;
  swapTotal: number;
  swapUsed: number;
  usagePercent: number;
}

export interface DiskMetrics {
  filesystem: string;
  mount: string;
  total: number;
  used: number;
  available: number;
  usagePercent: number;
}

export interface NetworkMetrics {
  interface: string;
  rxBytes: number;
  txBytes: number;
  rxPackets: number;
  txPackets: number;
}

export interface LoadMetrics {
  load1: number;
  load5: number;
  load15: number;
}

export interface ServerMetrics {
  timestamp: number;
  cpu: CPUMetrics;
  memory: MemoryMetrics;
  disk: DiskMetrics[];
  network: NetworkMetrics[];
  load: LoadMetrics;
  uptime: number;
  processCount: number;
  temperature?: number;
}

export interface DiskUsage {
  filesystem: string;
  mount: string;
  total: number;
  used: number;
  available: number;
  usagePercent: number;
}

export interface NetworkInterface {
  interface: string;
  rxBytes: number;
  txBytes: number;
  rxPackets: number;
  txPackets: number;
}

export interface ProcessInfo {
  pid: number;
  user: string;
  cpu: number;
  memory: number;
  command: string;
}

export type MetricsHistory = ServerMetrics[];
