import { api } from './client';

// File info type matching backend FileInfo
export interface FileInfo {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  mode: string;
  modTime: string;
  owner: string;
  group: string;
  isSymlink: boolean;
  linkTarget?: string;
}

// List directory response
export interface ListDirectoryResponse {
  path: string;
  files: FileInfo[];
  total: number;
  hasMore: boolean;
}

// Read file response
export interface ReadFileResponse {
  path: string;
  content: string;
  isBinary: boolean;
  size: number;
  mimeType: string;
  encoding: string;
  lineCount: number;
  truncated: boolean;
  maxLines: number;
}

// Write file request
export interface WriteFileRequest {
  path: string;
  content: string;
  isBinary?: boolean;
  mode?: string;
  overwrite?: boolean;
}

// Write file response
export interface WriteFileResponse {
  path: string;
  size: number;
  message: string;
}

// Delete request
export interface DeleteRequest {
  path: string;
  recursive?: boolean;
}

// Delete response
export interface DeleteResponse {
  path: string;
  message: string;
}

// Rename request
export interface RenameRequest {
  oldPath: string;
  newPath: string;
}

// Rename response
export interface RenameResponse {
  oldPath: string;
  newPath: string;
  message: string;
}

// Mkdir request
export interface MkdirRequest {
  path: string;
  mode?: string;
}

// Mkdir response
export interface MkdirResponse {
  path: string;
  message: string;
}

// Upload response
export interface UploadResponse {
  path: string;
  size: number;
  message: string;
}

/**
 * List directory contents on a remote server
 */
export async function listFiles(
  serverId: number,
  path: string = '/',
  showHidden: boolean = false
): Promise<ListDirectoryResponse> {
  const params = new URLSearchParams({ path });
  if (showHidden) params.set('hidden', 'true');
  
  return api.get<ListDirectoryResponse>(`/servers/${serverId}/files?${params.toString()}`);
}

/**
 * Get file content for preview
 */
export async function getFileContent(
  serverId: number,
  path: string,
  maxSize?: number
): Promise<ReadFileResponse> {
  const params = new URLSearchParams({ path });
  if (maxSize) params.set('maxSize', maxSize.toString());
  
  return api.get<ReadFileResponse>(`/servers/${serverId}/files/content?${params.toString()}`);
}

/**
 * Download a file from a remote server
 */
export async function downloadFile(
  serverId: number,
  path: string
): Promise<{ content: Blob; filename: string }> {
  const params = new URLSearchParams({ path });
  const response = await fetch(`/api/servers/${serverId}/files/download?${params.toString()}`, {
    headers: {
      'Authorization': `Bearer ${localStorage.getItem('meshium_session_token') || ''}`
    }
  });
  
  if (!response.ok) {
    throw new Error(`Download failed: ${response.statusText}`);
  }
  
  const contentDisposition = response.headers.get('Content-Disposition');
  let filename = path.split('/').pop() || 'download';
  if (contentDisposition) {
    const match = contentDisposition.match(/filename="(.+)"/);
    if (match) filename = match[1];
  }
  
  const content = await response.blob();
  return { content, filename };
}

/**
 * Upload a file to a remote server
 */
export async function uploadFile(
  serverId: number,
  path: string,
  file: File,
  overwrite: boolean = false,
  mode?: string
): Promise<UploadResponse> {
  const formData = new FormData();
  formData.append('file', file);
  formData.append('path', path);
  if (overwrite) formData.append('overwrite', 'true');
  if (mode) formData.append('mode', mode);
  
  const response = await fetch(`/api/servers/${serverId}/files/upload`, {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${localStorage.getItem('meshium_session_token') || ''}`
    },
    body: formData
  });
  
  if (!response.ok) {
    const error = await response.json();
    throw new Error(error.message || 'Upload failed');
  }
  
  return response.json();
}

/**
 * Write content to a file on a remote server
 */
export async function writeFile(
  serverId: number,
  request: WriteFileRequest
): Promise<WriteFileResponse> {
  return api.post<WriteFileResponse>(`/servers/${serverId}/files`, request);
}

/**
 * Delete a file or directory on a remote server
 */
export async function deleteFile(
  serverId: number,
  path: string,
  recursive: boolean = false
): Promise<DeleteResponse> {
  const params = new URLSearchParams({ path });
  if (recursive) params.set('recursive', 'true');
  
  return api.delete<DeleteResponse>(`/servers/${serverId}/files?${params.toString()}`);
}

/**
 * Rename or move a file on a remote server
 */
export async function renameFile(
  serverId: number,
  request: RenameRequest
): Promise<RenameResponse> {
  return api.put<RenameResponse>(`/servers/${serverId}/files`, request);
}

/**
 * Create a directory on a remote server
 */
export async function mkdir(
  serverId: number,
  request: MkdirRequest
): Promise<MkdirResponse> {
  return api.post<MkdirResponse>(`/servers/${serverId}/files/mkdir`, request);
}

/**
 * Get file/directory stats
 */
export async function statFile(
  serverId: number,
  path: string
): Promise<FileInfo> {
  const params = new URLSearchParams({ path });
  return api.get<FileInfo>(`/servers/${serverId}/files/stat?${params.toString()}`);
}

/**
 * Format file size for display
 */
export function formatFileSize(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

/**
 * Get file icon based on type/extension
 */
export function getFileIcon(file: FileInfo): string {
  if (file.isDir) return 'folder';
  if (file.isSymlink) return 'link';
  
  const ext = file.name.split('.').pop()?.toLowerCase() || '';
  
  // Image files
  if (['jpg', 'jpeg', 'png', 'gif', 'bmp', 'svg', 'webp'].includes(ext)) return 'image';
  
  // Code files
  if (['js', 'ts', 'jsx', 'tsx', 'py', 'rb', 'go', 'rs', 'java', 'c', 'cpp', 'h', 'cs'].includes(ext)) return 'code';
  
  // Config files
  if (['json', 'yaml', 'yml', 'toml', 'ini', 'conf', 'cfg'].includes(ext)) return 'settings';
  
  // Document files
  if (['md', 'txt', 'rst', 'pdf', 'doc', 'docx'].includes(ext)) return 'file-text';
  
  // Archive files
  if (['zip', 'tar', 'gz', 'bz2', 'xz', '7z', 'rar'].includes(ext)) return 'archive';
  
  // Executable files
  if (['sh', 'bash', 'zsh', 'exe', 'bat', 'cmd'].includes(ext)) return 'terminal';
  
  return 'file';
}

/**
 * Check if file is previewable (text-based)
 */
export function isPreviewable(file: FileInfo): boolean {
  if (file.isDir) return false;
  
  const ext = file.name.split('.').pop()?.toLowerCase() || '';
  const textExtensions = [
    'txt', 'md', 'json', 'yaml', 'yml', 'toml', 'ini', 'conf', 'cfg',
    'js', 'ts', 'jsx', 'tsx', 'py', 'rb', 'go', 'rs', 'java', 'c', 'cpp', 'h', 'cs',
    'sh', 'bash', 'zsh', 'ps1', 'bat', 'cmd',
    'html', 'css', 'scss', 'sass', 'less',
    'xml', 'svg', 'sql',
    'gitignore', 'dockerfile', 'makefile', 'readme', 'license'
  ];
  
  return textExtensions.includes(ext) || file.name.startsWith('.') || file.name.toLowerCase().includes('readme');
}
