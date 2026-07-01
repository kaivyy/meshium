import { api } from '$lib/api/client';

export interface DockerPortMapping {
  hostPort: number;
  containerPort: number;
  protocol: string;
}

export interface DockerContainer {
  id: string;
  name: string;
  image: string;
  state: string;
  status: string;
  ports: DockerPortMapping[];
}

export interface DockerImage {
  id: string;
  repository: string;
  tag: string;
  size: string;
}

export interface DockerContainersResponse {
  containers: DockerContainer[];
  total: number;
}

export interface DockerImagesResponse {
  images: DockerImage[];
  total: number;
}

export interface DockerActionResponse {
  name: string;
  action: string;
  message: string;
}

export interface DockerPullResponse {
  image: string;
  output: string;
}

export interface DockerLogsResponse {
  name: string;
  lines: number;
  logs: string;
}

export const dockerApi = {
  listContainers: (serverId: number) =>
    api.get<DockerContainersResponse>(`/servers/${serverId}/docker/containers`),
  listImages: (serverId: number) =>
    api.get<DockerImagesResponse>(`/servers/${serverId}/docker/images`),
  startContainer: (serverId: number, name: string) =>
    api.post<DockerActionResponse>(`/servers/${serverId}/docker/containers/${encodeURIComponent(name)}/start`, {}),
  stopContainer: (serverId: number, name: string) =>
    api.post<DockerActionResponse>(`/servers/${serverId}/docker/containers/${encodeURIComponent(name)}/stop`, {}),
  restartContainer: (serverId: number, name: string) =>
    api.post<DockerActionResponse>(`/servers/${serverId}/docker/containers/${encodeURIComponent(name)}/restart`, {}),
  removeContainer: (serverId: number, name: string, force = false) =>
    api.delete<DockerActionResponse>(`/servers/${serverId}/docker/containers/${encodeURIComponent(name)}?force=${force}`),
  pullImage: (serverId: number, image: string) =>
    api.post<DockerPullResponse>(`/servers/${serverId}/docker/pull`, { image }),
  getContainerLogs: (serverId: number, name: string, lines = 100) =>
    api.get<DockerLogsResponse>(`/servers/${serverId}/docker/containers/${encodeURIComponent(name)}/logs?lines=${lines}`),
};
