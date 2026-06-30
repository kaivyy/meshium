<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import {
    Folder, FolderOpen, FileText, File, FileCode, FileImage, FileArchive,
    FileTerminal, Link, ChevronRight, Home, RefreshCw, Upload, Download,
    Trash2, Edit3, FolderPlus, Search, ArrowLeft, Eye, X, Check, AlertCircle,
    ChevronUp, ChevronDown, MoreVertical, Copy, Move, Save
  } from 'lucide-svelte';
  import { Badge, Card, EmptyState, PageHeader, Skeleton, Spinner, Modal } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';
  import { type Server } from '$lib/stores/servers';
  import { api } from '$lib/api/client';
  import {
    listFiles, getFileContent, downloadFile, uploadFile, deleteFile, renameFile, mkdir, writeFile,
    type FileInfo, type ReadFileResponse, formatFileSize, isPreviewable
  } from '$lib/api/files';

  // Route params
  let serverId = parseInt($page.params.id);
  let server: Server | null = null;

  // State
  let currentPath = $state('/');
  let files = $state([] as FileInfo[]);
  let loading = $state(true);
  let searchQuery = $state('');
  let showHidden = $state(false);
  let sortBy = $state<'name' | 'size' | 'modTime'>('name');
  let sortDir = $state<'asc' | 'desc'>('asc');
  let selectedFile = $state<FileInfo | null>(null);
  let fileContent = $state<ReadFileResponse | null>(null);
  let loadingContent = $state(false);
  let showPreview = $state(false);
  let showUploadModal = $state(false);
  let showMkdirModal = $state(false);
  let showRenameModal = $state(false);
  let showDeleteModal = $state(false);
  let renameNewPath = $state('');
  let mkdirPath = $state('');
  let uploadFile_input = $state<File | null>(null);
  let uploadPath = $state('');
  let uploadOverwrite = $state(false);
  let uploading = $state(false);
  let actionTarget = $state<FileInfo | null>(null);

  // Editor state
  let showEditModal = $state(false);
  let editFile = $state<FileInfo | null>(null);
  let editContent = $state('');
  let editOriginalContent = $state('');
  let editIsBinary = $state(false);
  let editMimeType = $state('');
  let editLoading = $state(false);
  let editSaving = $state(false);

  // Derived: dirty flag
  let editDirty = $derived(editContent !== editOriginalContent);

  // Derived: breadcrumbs
  let breadcrumbs = $derived.by(() => {
    const parts = currentPath.split('/').filter(Boolean);
    let path = '';
    return [
      { name: 'Root', path: '/' },
      ...parts.map(p => {
        path += '/' + p;
        return { name: p, path };
      })
    ];
  });

  // Derived: filtered and sorted files
  let filteredFiles = $derived.by(() => {
    let result = [...files];
    
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      result = result.filter(f => f.name.toLowerCase().includes(q));
    }
    
    if (!showHidden) {
      result = result.filter(f => !f.name.startsWith('.'));
    }
    
    result.sort((a, b) => {
      // Directories first
      if (a.isDir && !b.isDir) return -1;
      if (!a.isDir && b.isDir) return 1;
      
      let cmp = 0;
      if (sortBy === 'name') cmp = a.name.localeCompare(b.name);
      else if (sortBy === 'size') cmp = a.size - b.size;
      else if (sortBy === 'modTime') cmp = new Date(a.modTime).getTime() - new Date(b.modTime).getTime();
      
      return sortDir === 'asc' ? cmp : -cmp;
    });
    
    return result;
  });

  // Derived: stats
  let stats = $derived.by(() => {
    const dirs = filteredFiles.filter(f => f.isDir).length;
    const filesCount = filteredFiles.filter(f => !f.isDir).length;
    const totalSize = filteredFiles.filter(f => !f.isDir).reduce((sum, f) => sum + f.size, 0);
    return { dirs, filesCount, totalSize };
  });

  onMount(async () => {
    await loadServer();
    await loadFiles();
  });

  async function loadServer() {
    try {
      const servers = await api.get('/servers') as Server[];
      server = servers.find(s => s.id === serverId) || null;
    } catch {
      toast.error('Failed to load server info');
    }
  }

  async function loadFiles() {
    loading = true;
    selectedFile = null;
    fileContent = null;
    showPreview = false;
    try {
      const resp = await listFiles(serverId, currentPath, showHidden);
      files = resp.files || [];
    } catch (err: any) {
      if (err?.message?.includes('not found')) {
        toast.error('Directory not found');
        currentPath = '/';
        await loadFiles();
        return;
      }
      toast.error('Failed to load files: ' + (err?.message || 'unknown error'));
      files = [];
    } finally {
      loading = false;
    }
  }

  async function navigateTo(path: string) {
    currentPath = path;
    await loadFiles();
  }

  async function openFile(file: FileInfo) {
    if (file.isDir) {
      await navigateTo(file.path);
    } else {
      selectedFile = file;
      showPreview = true;
      await loadFileContent(file);
    }
  }

  async function loadFileContent(file: FileInfo) {
    loadingContent = true;
    fileContent = null;
    try {
      fileContent = await getFileContent(serverId, file.path);
    } catch (err: any) {
      toast.error('Failed to read file: ' + (err?.message || 'unknown error'));
    } finally {
      loadingContent = false;
    }
  }

  async function handleDownload(file: FileInfo) {
    try {
      const { content, filename } = await downloadFile(serverId, file.path);
      const url = URL.createObjectURL(content);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      a.click();
      URL.revokeObjectURL(url);
      toast.success(`Downloaded ${filename}`);
    } catch (err: any) {
      toast.error('Download failed: ' + (err?.message || 'unknown error'));
    }
  }

  async function handleDelete() {
    if (!actionTarget) return;
    try {
      await deleteFile(serverId, actionTarget.path, actionTarget.isDir);
      toast.success(`Deleted ${actionTarget.name}`);
      showDeleteModal = false;
      actionTarget = null;
      await loadFiles();
    } catch (err: any) {
      toast.error('Delete failed: ' + (err?.message || 'unknown error'));
    }
  }

  async function handleRename() {
    if (!actionTarget || !renameNewPath) return;
    try {
      await renameFile(serverId, { oldPath: actionTarget.path, newPath: renameNewPath });
      toast.success(`Renamed to ${renameNewPath}`);
      showRenameModal = false;
      actionTarget = null;
      renameNewPath = '';
      await loadFiles();
    } catch (err: any) {
      toast.error('Rename failed: ' + (err?.message || 'unknown error'));
    }
  }

  async function handleMkdir() {
    if (!mkdirPath) return;
    try {
      const fullPath = currentPath === '/' ? '/' + mkdirPath : currentPath + '/' + mkdirPath;
      await mkdir(serverId, { path: fullPath });
      toast.success(`Created directory ${mkdirPath}`);
      showMkdirModal = false;
      mkdirPath = '';
      await loadFiles();
    } catch (err: any) {
      toast.error('Create directory failed: ' + (err?.message || 'unknown error'));
    }
  }

  async function handleUpload() {
    if (!uploadFile_input || !uploadPath) return;
    uploading = true;
    try {
      const fullPath = currentPath === '/' ? '/' + uploadPath : currentPath + '/' + uploadPath;
      await uploadFile(serverId, fullPath, uploadFile_input, uploadOverwrite);
      toast.success(`Uploaded ${uploadFile_input.name}`);
      showUploadModal = false;
      uploadFile_input = null;
      uploadPath = '';
      uploadOverwrite = false;
      await loadFiles();
    } catch (err: any) {
      toast.error('Upload failed: ' + (err?.message || 'unknown error'));
    } finally {
      uploading = false;
    }
  }

  function openRenameModal(file: FileInfo) {
    actionTarget = file;
    renameNewPath = file.path;
    showRenameModal = true;
  }

  function openDeleteModal(file: FileInfo) {
    actionTarget = file;
    showDeleteModal = true;
  }

  async function openEditor(file: FileInfo) {
    editFile = file;
    editLoading = true;
    editContent = '';
    editOriginalContent = '';
    editIsBinary = false;
    editMimeType = '';
    showEditModal = true;

    try {
      const resp = await getFileContent(serverId, file.path);
      if (resp.isBinary) {
        editIsBinary = true;
        editMimeType = resp.mimeType;
      } else {
        editContent = resp.content;
        editOriginalContent = resp.content;
        editMimeType = resp.mimeType;
      }
    } catch (err: any) {
      toast.error('Failed to load file: ' + (err?.message || 'unknown error'));
      showEditModal = false;
    } finally {
      editLoading = false;
    }
  }

  async function handleSaveEdit() {
    if (!editFile || !editDirty) return;
    editSaving = true;
    try {
      await writeFile(serverId, {
        path: editFile.path,
        content: editContent,
        overwrite: true
      });
      toast.success(`Saved ${editFile.name}`);
      editOriginalContent = editContent;
      showEditModal = false;
      editFile = null;
      await loadFiles();
    } catch (err: any) {
      toast.error('Save failed: ' + (err?.message || 'unknown error'));
    } finally {
      editSaving = false;
    }
  }

  function handleEditKeydown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault();
      if (editDirty && !editSaving) {
        handleSaveEdit();
      }
    }
  }

  function toggleSort(field: 'name' | 'size' | 'modTime') {
    if (sortBy === field) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      sortBy = field;
      sortDir = 'asc';
    }
  }

  function getFileIconComponent(file: FileInfo) {
    if (file.isDir) return Folder;
    if (file.isSymlink) return Link;
    
    const ext = file.name.split('.').pop()?.toLowerCase() || '';
    if (['jpg', 'jpeg', 'png', 'gif', 'bmp', 'svg', 'webp'].includes(ext)) return FileImage;
    if (['js', 'ts', 'jsx', 'tsx', 'py', 'rb', 'go', 'rs', 'java', 'c', 'cpp', 'h', 'cs', 'sh', 'bash'].includes(ext)) return FileCode;
    if (['zip', 'tar', 'gz', 'bz2', 'xz', '7z', 'rar'].includes(ext)) return FileArchive;
    if (['sh', 'bash', 'zsh', 'exe', 'bat', 'cmd'].includes(ext)) return FileTerminal;
    if (['md', 'txt', 'rst', 'pdf', 'doc', 'docx'].includes(ext)) return FileText;
    return File;
  }

  function formatDate(dateStr: string): string {
    const date = new Date(dateStr);
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit' });
  }
</script>

<svelte:head><title>File Browser - {server?.name || 'Server'} - Meshium</title></svelte:head>

<div class="p-4 sm:p-6 max-w-7xl mx-auto">
  <PageHeader title="File Browser" subtitle={server?.name || `Server #${serverId}`}>
    {#snippet actions()}
      <div class="flex items-center gap-2">
        <button type="button" onclick={() => showMkdirModal = true} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">
          <FolderPlus size={16} /> New Folder
        </button>
        <button type="button" onclick={() => showUploadModal = true} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">
          <Upload size={16} /> Upload
        </button>
        <button type="button" onclick={loadFiles} disabled={loading} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60">
          {#if loading}<Spinner size="sm" label="" />{:else}<RefreshCw size={16} />{/if}
          Refresh
        </button>
      </div>
    {/snippet}
  </PageHeader>

  <!-- Breadcrumbs -->
  <div class="mb-4 flex items-center gap-1 overflow-x-auto whitespace-nowrap rounded-lg border border-slate-200 bg-white px-3 py-2">
    {#each breadcrumbs as crumb, i}
      <button type="button" onclick={() => navigateTo(crumb.path)} class="inline-flex items-center gap-1 text-sm hover:text-blue-600 hover:underline">
        {#if i === 0}<Home size={14} />{/if}
        {crumb.name}
      </button>
      {#if i < breadcrumbs.length - 1}<ChevronRight size={14} class="text-slate-400" />{/if}
    {/each}
  </div>

  <!-- Search and filter bar -->
  <div class="mb-4 flex items-center gap-3">
    <div class="relative flex-1">
      <Search size={16} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
      <input type="text" bind:value={searchQuery} placeholder="Search files..." class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-sm outline-none focus:border-blue-500" />
    </div>
    <label class="inline-flex items-center gap-2 text-sm text-slate-600">
      <input type="checkbox" bind:checked={showHidden} onchange={loadFiles} class="rounded" />
      Show hidden
    </label>
  </div>

  <!-- Stats -->
  <div class="mb-4 flex items-center gap-4 text-xs text-slate-500">
    <span>{stats.dirs} folders</span>
    <span>·</span>
    <span>{stats.filesCount} files</span>
    <span>·</span>
    <span>{formatFileSize(stats.totalSize)} total</span>
  </div>

  <!-- File list -->
  {#if loading}
    <div class="space-y-2">
      {#each Array(8) as _}
        <Card><div class="flex items-center gap-3 p-3"><Skeleton width="2rem" /><Skeleton width="40%" /><Skeleton width="10%" /></div></Card>
      {/each}
    </div>
  {:else if filteredFiles.length === 0}
    <EmptyState title="No files found" description={searchQuery ? "No files match your search." : "This directory is empty."} icon={emptyIcon} />
  {:else}
    <Card padding="none">
      <!-- Table header -->
      <div class="flex items-center gap-3 border-b border-slate-200 bg-slate-50 px-4 py-2 text-xs font-semibold uppercase tracking-wide text-slate-500">
        <button type="button" onclick={() => toggleSort('name')} class="flex flex-1 items-center gap-1 hover:text-slate-700">
          Name
          {#if sortBy === 'name'}{#if sortDir === 'asc'}<ChevronUp size={12} />{:else}<ChevronDown size={12} />{/if}{/if}
        </button>
        <button type="button" onclick={() => toggleSort('size')} class="flex w-24 items-center gap-1 hover:text-slate-700">
          Size
          {#if sortBy === 'size'}{#if sortDir === 'asc'}<ChevronUp size={12} />{:else}<ChevronDown size={12} />{/if}{/if}
        </button>
        <div class="hidden w-32 sm:block">Owner</div>
        <div class="hidden w-32 sm:block">Modified</div>
        <div class="w-24 text-right">Actions</div>
      </div>

      <!-- File rows -->
      {#each filteredFiles as file (file.path)}
        {@const Icon = getFileIconComponent(file)}
        <div class="flex items-center gap-3 border-b border-slate-100 px-4 py-2 hover:bg-slate-50 transition-colors">
          <!-- Name -->
          <button type="button" onclick={() => openFile(file)} class="flex flex-1 items-center gap-2 text-left">
            <Icon size={18} class={file.isDir ? 'text-blue-500' : 'text-slate-400'} />
            <span class="text-sm font-medium text-slate-900">{file.name}</span>
            {#if file.isSymlink && file.linkTarget}
              <span class="text-xs text-slate-400">→ {file.linkTarget}</span>
            {/if}
          </button>
          
          <!-- Size -->
          <div class="w-24 text-sm text-slate-500">
            {file.isDir ? '—' : formatFileSize(file.size)}
          </div>
          
          <!-- Owner -->
          <div class="hidden w-32 text-sm text-slate-500 sm:block">{file.owner}</div>
          
          <!-- Modified -->
          <div class="hidden w-32 text-xs text-slate-400 sm:block">{formatDate(file.modTime)}</div>
          
          <!-- Actions -->
          <div class="flex w-28 items-center justify-end gap-1">
            {#if !file.isDir}
              <button type="button" onclick={() => openEditor(file)} title="Edit" class="rounded p-1.5 text-slate-400 hover:bg-slate-100 hover:text-green-600">
                <Edit3 size={14} />
              </button>
              <button type="button" onclick={() => handleDownload(file)} title="Download" class="rounded p-1.5 text-slate-400 hover:bg-slate-100 hover:text-blue-600">
                <Download size={14} />
              </button>
            {/if}
            <button type="button" onclick={() => openRenameModal(file)} title="Rename" class="rounded p-1.5 text-slate-400 hover:bg-slate-100 hover:text-blue-600">
              <Move size={14} />
            </button>
            <button type="button" onclick={() => openDeleteModal(file)} title="Delete" class="rounded p-1.5 text-slate-400 hover:bg-slate-100 hover:text-red-600">
              <Trash2 size={14} />
            </button>
          </div>
        </div>
      {/each}
    </Card>
  {/if}
</div>

<!-- File Preview Modal -->
{#if showPreview && selectedFile}
  <Modal title={selectedFile.name} onClose={() => { showPreview = false; selectedFile = null; fileContent = null; }}>
    <div class="max-h-[60vh] overflow-auto">
      {#if loadingContent}
        <div class="flex items-center justify-center py-8"><Spinner label="Loading file..." /></div>
      {:else if fileContent}
        {#if fileContent.isBinary}
          <div class="py-8 text-center">
            <AlertCircle size={32} class="mx-auto text-slate-400" />
            <p class="mt-2 text-sm text-slate-500">Binary file — preview not available</p>
            <p class="text-xs text-slate-400">{fileContent.mimeType} · {formatFileSize(fileContent.size)}</p>
          </div>
        {:else}
          <pre class="rounded-lg bg-slate-900 p-4 text-sm text-slate-100 overflow-auto"><code>{fileContent.content}</code></pre>
          {#if fileContent.truncated}
            <p class="mt-2 text-xs text-amber-600">File truncated — showing first {fileContent.maxLines} lines of {fileContent.lineCount}+ lines</p>
          {/if}
        {/if}
      {:else}
        <p class="py-8 text-center text-sm text-slate-500">Failed to load file content</p>
      {/if}
    </div>
    <div class="mt-4 flex items-center justify-between border-t border-slate-200 pt-3">
      <div class="text-xs text-slate-500">
        {formatFileSize(fileContent?.size || selectedFile.size)} · {fileContent?.mimeType || 'unknown'}
      </div>
      <div class="flex items-center gap-2">
        {#if fileContent && !fileContent.isBinary}
          <button type="button" onclick={() => { const f = selectedFile; showPreview = false; selectedFile = null; fileContent = null; openEditor(f); }} class="inline-flex items-center gap-2 rounded-lg bg-green-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-green-700">
            <Edit3 size={14} /> Edit
          </button>
        {/if}
        <button type="button" onclick={() => handleDownload(selectedFile)} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50">
          <Download size={14} /> Download
        </button>
        <button type="button" onclick={() => { showPreview = false; selectedFile = null; fileContent = null; }} class="inline-flex items-center gap-2 rounded-lg bg-slate-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-800">
          Close
        </button>
      </div>
    </div>
  </Modal>
{/if}

<!-- Upload Modal -->
{#if showUploadModal}
  <Modal title="Upload File" onClose={() => { showUploadModal = false; uploadFile_input = null; uploadPath = ''; }}>
    <div class="space-y-4">
      <div>
        <label class="mb-1 block text-sm font-medium text-slate-700">File</label>
        <input type="file" onchange={(e) => { const target = e.target as HTMLInputElement; uploadFile_input = target.files?.[0] || null; if (uploadFile_input && !uploadPath) uploadPath = uploadFile_input.name; }} class="block w-full text-sm text-slate-500" />
      </div>
      <div>
        <label class="mb-1 block text-sm font-medium text-slate-700">Remote Path</label>
        <div class="flex items-center gap-2">
          <span class="text-sm text-slate-500">{currentPath === '/' ? '/' : currentPath + '/'}</span>
          <input type="text" bind:value={uploadPath} placeholder="filename" class="flex-1 rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-blue-500" />
        </div>
      </div>
      <label class="flex items-center gap-2 text-sm text-slate-600">
        <input type="checkbox" bind:checked={uploadOverwrite} class="rounded" />
        Overwrite if exists
      </label>
    </div>
    <div class="mt-4 flex justify-end gap-2">
      <button type="button" onclick={() => { showUploadModal = false; uploadFile_input = null; uploadPath = ''; }} class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">Cancel</button>
      <button type="button" onclick={handleUpload} disabled={!uploadFile_input || !uploadPath || uploading} class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60">
        {#if uploading}<Spinner size="sm" label="" />{:else}<Upload size={14} />{/if}
        Upload
      </button>
    </div>
  </Modal>
{/if}

<!-- New Folder Modal -->
{#if showMkdirModal}
  <Modal title="New Folder" onClose={() => { showMkdirModal = false; mkdirPath = ''; }}>
    <div class="space-y-4">
      <div>
        <label class="mb-1 block text-sm font-medium text-slate-700">Folder Name</label>
        <div class="flex items-center gap-2">
          <span class="text-sm text-slate-500">{currentPath === '/' ? '/' : currentPath + '/'}</span>
          <input type="text" bind:value={mkdirPath} placeholder="folder-name" class="flex-1 rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-blue-500" onkeydown={(e) => { if (e.key === 'Enter') handleMkdir(); }} />
        </div>
      </div>
    </div>
    <div class="mt-4 flex justify-end gap-2">
      <button type="button" onclick={() => { showMkdirModal = false; mkdirPath = ''; }} class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">Cancel</button>
      <button type="button" onclick={handleMkdir} disabled={!mkdirPath} class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60">
        <FolderPlus size={14} /> Create
      </button>
    </div>
  </Modal>
{/if}

<!-- Rename Modal -->
{#if showRenameModal && actionTarget}
  <Modal title="Rename / Move" onClose={() => { showRenameModal = false; actionTarget = null; renameNewPath = ''; }}>
    <div class="space-y-4">
      <div>
        <label class="mb-1 block text-sm font-medium text-slate-700">New Path</label>
        <input type="text" bind:value={renameNewPath} class="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-blue-500" onkeydown={(e) => { if (e.key === 'Enter') handleRename(); }} />
      </div>
      <p class="text-xs text-slate-500">Current: {actionTarget.path}</p>
    </div>
    <div class="mt-4 flex justify-end gap-2">
      <button type="button" onclick={() => { showRenameModal = false; actionTarget = null; renameNewPath = ''; }} class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">Cancel</button>
      <button type="button" onclick={handleRename} disabled={!renameNewPath} class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60">
        <Check size={14} /> Rename
      </button>
    </div>
  </Modal>
{/if}

<!-- Delete Confirmation Modal -->
{#if showDeleteModal && actionTarget}
  <Modal title="Confirm Delete" onClose={() => { showDeleteModal = false; actionTarget = null; }}>
    <div class="space-y-3">
      <div class="flex items-start gap-3">
        <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-red-50 text-red-600"><AlertCircle size={20} /></div>
        <div>
          <p class="text-sm font-medium text-slate-900">Delete {actionTarget.isDir ? 'folder' : 'file'}: {actionTarget.name}</p>
          <p class="mt-1 text-sm text-slate-500">
            {#if actionTarget.isDir}
              This will delete the folder and all its contents recursively.
            {:else}
              This action cannot be undone.
            {/if}
          </p>
        </div>
      </div>
    </div>
    <div class="mt-4 flex justify-end gap-2">
      <button type="button" onclick={() => { showDeleteModal = false; actionTarget = null; }} class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">Cancel</button>
      <button type="button" onclick={handleDelete} class="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700">
        <Trash2 size={14} /> Delete
      </button>
    </div>
  </Modal>
{/if}

<!-- Edit File Modal -->
{#if showEditModal && editFile}
  <Modal title="Edit File" onClose={() => {
    if (editDirty && !confirm('Discard unsaved changes?')) return;
    showEditModal = false; editFile = null; editContent = ''; editOriginalContent = '';
  }}>
    <div class="space-y-3">
      <!-- File info bar -->
      <div class="flex items-center justify-between rounded-lg bg-slate-50 px-3 py-2">
        <div class="flex items-center gap-2">
          <FileText size={16} class="text-slate-400" />
          <span class="font-mono text-sm text-slate-700">{editFile.path}</span>
        </div>
        <div class="flex items-center gap-3 text-xs text-slate-500">
          <span>{editMimeType}</span>
          <span>·</span>
          <span>{formatFileSize(editFile.size)}</span>
          {#if editDirty}
            <span class="inline-flex items-center gap-1 text-amber-600">
              <span class="h-2 w-2 rounded-full bg-amber-500"></span>
              Unsaved
            </span>
          {:else}
            <span class="inline-flex items-center gap-1 text-green-600">
              <span class="h-2 w-2 rounded-full bg-green-500"></span>
              Saved
            </span>
          {/if}
        </div>
      </div>

      {#if editLoading}
        <div class="flex items-center justify-center py-12"><Spinner label="Loading file..." /></div>
      {:else if editIsBinary}
        <div class="py-12 text-center">
          <AlertCircle size={32} class="mx-auto text-slate-400" />
          <p class="mt-2 text-sm text-slate-500">Binary file cannot be edited</p>
          <p class="text-xs text-slate-400">{editMimeType}</p>
        </div>
      {:else}
        <!-- Editor textarea with line numbers -->
        <div class="relative overflow-hidden rounded-lg border border-slate-300">
          <div class="flex" style="max-height: 60vh;">
            <!-- Line numbers gutter -->
            <div id="line-numbers" class="select-none overflow-hidden bg-slate-50 py-3 text-right font-mono text-xs text-slate-400" style="min-width: 3rem; max-height: 60vh;">
              {#each editContent.split('\n') as _, i}
                <div class="px-2 leading-6">{i + 1}</div>
              {/each}
            </div>
            <!-- Textarea -->
            <textarea
              bind:value={editContent}
              onkeydown={handleEditKeydown}
              onscroll={(e) => {
                const ln = document.getElementById('line-numbers');
                if (ln) { ln.scrollTop = (e.target as HTMLTextAreaElement).scrollTop; }
              }}
              id="edit-textarea"
              class="flex-1 resize-none overflow-auto bg-white p-3 font-mono text-sm leading-6 text-slate-800 outline-none"
              style="max-height: 60vh; min-height: 300px;"
              spellcheck="false"
              autocomplete="off"
              autocorrect="off"
              autocapitalize="off"
              placeholder="File content..."
            ></textarea>
          </div>
        </div>
        <p class="text-xs text-slate-400">
          Press <kbd class="rounded border border-slate-300 bg-slate-50 px-1.5 py-0.5 font-mono text-[10px]">Ctrl+S</kbd> to save · {editContent.split('\n').length} lines
        </p>
      {/if}
    </div>

    {#if !editLoading && !editIsBinary}
      <div class="mt-4 flex items-center justify-between border-t border-slate-200 pt-3">
        <div class="text-xs text-slate-500">
          {#if editDirty}
            <span class="text-amber-600">Unsaved changes</span>
          {:else}
            <span class="text-green-600">All changes saved</span>
          {/if}
        </div>
        <div class="flex items-center gap-2">
          <button type="button" onclick={() => {
            if (editDirty && !confirm('Discard unsaved changes?')) return;
            showEditModal = false; editFile = null; editContent = ''; editOriginalContent = '';
          }} class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">
            Cancel
          </button>
          <button type="button" onclick={handleSaveEdit} disabled={!editDirty || editSaving} class="inline-flex items-center gap-2 rounded-lg bg-green-600 px-4 py-2 text-sm font-medium text-white hover:bg-green-700 disabled:opacity-60">
            {#if editSaving}<Spinner size="sm" label="" />{:else}<Save size={14} />{/if}
            Save
          </button>
        </div>
      </div>
    {/if}
  </Modal>
{/if}

{#snippet emptyIcon()}<Folder size={22} />{/snippet}
