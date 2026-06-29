import { api } from '$lib/api/client';

// --- Snapshot Types (matching backend internal/mod/discovery/snapshot.go) ---

export interface CollectorError {
  collector: string;
  error: string;
}

export interface OSInfo {
  distro: string;
  kernel: string;
  architecture: string;
  timezone: string;
  uptimeSeconds: number;
  hostname: string;
  virtualization: string;
}

export interface HardwareInfo {
  cpuModel: string;
  cpuCores: number;
  ramTotalMb: number;
  ramUsedMb: number;
  diskTotalGb: number;
  diskUsedGb: number;
}

export interface PortMapping {
  hostPort: number;
  containerPort: number;
  protocol: string;
}

export interface ContainerInfo {
  name: string;
  image: string;
  status: string;
  state: string;
  ports: PortMapping[];
  volumes: string[];
  networks: string[];
  labels: Record<string, string>;
}

export interface ImageInfo {
  repository: string;
  tag: string;
  id: string;
  size: string;
}

export interface ComposeProject {
  name: string;
  configFiles: string;
  services: string[];
}

export interface DockerInfo {
  version: string;
  containers: ContainerInfo[];
  images: ImageInfo[];
  composeProjects: ComposeProject[];
}

export interface SystemService {
  name: string;
  description: string;
  loadState: string;
  activeState: string;
  subState: string;
  type: string;
  dependsOn: string[];
}

export interface DatabaseInfo {
  type: string; // mysql, postgresql, mongodb, redis
  version: string;
  port: number;
  processName: string;
  dataDir: string;
  sizeMb: number;
  running: boolean;
}

export interface NginxVHost {
  serverName: string;
  listen: string;
  root: string;
  proxyPass: string;
  configFile: string;
}

export interface UpstreamServer {
  address: string;
  weight: number;
}

export interface UpstreamConfig {
  name: string;
  servers: UpstreamServer[];
}

export interface SSLCert {
  domain: string;
  path: string;
  expiry: string;
  daysRemaining: number;
  issuer: string;
}

export interface NginxInfo {
  version: string;
  vhosts: NginxVHost[];
  upstreams: UpstreamConfig[];
  sslCerts: SSLCert[];
}

export interface DiskPartition {
  filesystem: string;
  mountPoint: string;
  sizeGb: number;
  usedGb: number;
  availGb: number;
  usePercent: number;
}

export interface OpenPort {
  port: number;
  protocol: string;
  process: string;
  pid: number;
  address: string;
}

export interface ServerSnapshot {
  capturedAt: string;
  os: OSInfo;
  hardware: HardwareInfo;
  docker: DockerInfo | null;
  services: SystemService[];
  databases: DatabaseInfo[];
  nginx: NginxInfo | null;
  diskUsage: DiskPartition[];
  networkPorts: OpenPort[];
  collectionErrors: CollectorError[];
}

// --- Connection/System Info (from /api/servers/{id}/info) ---

export interface ServerConnectionInfo {
  sshStatus: string;
  latencyMs: number;
  hostname: string;
  os: string;
  kernel: string;
  architecture: string;
  cpuModel: string;
  cpuCores: number;
  ramTotalMb: number;
  diskTotalGb: number;
  virtualization: string;
  provider: string;
  publicIp: string;
  privateIp: string;
  timezone: string;
}

// --- Compatibility Types (matching backend internal/mod/discovery/compat.go) ---

export interface CompatibilityBlocker {
  category: string;
  message: string;
}

export interface CompatibilityWarning {
  category: string;
  message: string;
}

export interface CompatibilityReport {
  compatible: boolean;
  blockers: CompatibilityBlocker[];
  warnings: CompatibilityWarning[];
}

// --- API ---

export const discoveryApi = {
  getSnapshot: (serverID: number) =>
    api.get(`/servers/${serverID}/snapshot`) as Promise<ServerSnapshot>,
  getInfo: (serverID: number) =>
    api.get(`/servers/${serverID}/info`) as Promise<ServerConnectionInfo>,
  triggerDiscovery: (serverID: number) =>
    api.post(`/servers/${serverID}/discover`, {}) as Promise<{ jobID: string }>,
  getCompatibility: (sourceID: number, targetID: number) =>
    api.get(`/compat?source=${sourceID}&target=${targetID}`) as Promise<CompatibilityReport>,
};
