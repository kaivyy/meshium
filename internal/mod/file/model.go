package file

import "time"

// FileInfo represents metadata about a file or directory.
type FileInfo struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`     // e.g., "-rw-r--r--", "drwxr-xr-x"
	ModTime  time.Time `json:"modTime"`
	Owner    string    `json:"owner"`    // owner username (if available)
	Group    string    `json:"group"`    // group name (if available)
	IsSymlink bool     `json:"isSymlink"`
	LinkTarget string   `json:"linkTarget,omitempty"` // symlink target (if IsSymlink)
}

// ListDirectoryRequest is the request for listing a directory.
type ListDirectoryRequest struct {
	Path           string `json:"path"`
	ShowHidden     bool   `json:"showHidden"`
	FollowSymlinks bool   `json:"followSymlinks"`
}

// ListDirectoryResponse is the response for listing a directory.
type ListDirectoryResponse struct {
	Path    string     `json:"path"`
	Files   []FileInfo `json:"files"`
	Total   int        `json:"total"`
	HasMore bool       `json:"hasMore"` // for pagination if needed
}

// ReadFileRequest is the request for reading a file.
type ReadFileRequest struct {
	Path string `json:"path"`
}

// ReadFileResponse is the response for reading a file.
type ReadFileResponse struct {
	Path        string `json:"path"`
	Content     string `json:"content"`     // base64 encoded for binary files
	IsBinary    bool   `json:"isBinary"`
	Size        int64  `json:"size"`
	MimeType    string `json:"mimeType"`
	Encoding    string `json:"encoding"`    // utf-8, binary, etc.
	LineCount   int    `json:"lineCount"`
	Truncated   bool   `json:"truncated"`    // true if file was too large
	MaxLines    int    `json:"maxLines"`     // max lines returned if truncated
}

// WriteFileRequest is the request for writing a file.
type WriteFileRequest struct {
	Path        string `json:"path"`
	Content     string `json:"content"`     // base64 encoded for binary files
	IsBinary    bool   `json:"isBinary"`
	Mode        string `json:"mode"`        // optional, e.g., "0644"
	Overwrite   bool   `json:"overwrite"`
}

// WriteFileResponse is the response for writing a file.
type WriteFileResponse struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Message string `json:"message"`
}

// DeleteRequest is the request for deleting a file or directory.
type DeleteRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"` // for directories
}

// DeleteResponse is the response for deleting a file.
type DeleteResponse struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// RenameRequest is the request for renaming/moving a file.
type RenameRequest struct {
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
}

// RenameResponse is the response for renaming a file.
type RenameResponse struct {
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
	Message string `json:"message"`
}

// MkdirRequest is the request for creating a directory.
type MkdirRequest struct {
	Path string `json:"path"`
	Mode string `json:"mode"` // optional, e.g., "0755"
}

// MkdirResponse is the response for creating a directory.
type MkdirResponse struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// CopyRequest is the request for copying a file between servers.
type CopyRequest struct {
	SourceServerID int    `json:"sourceServerId"`
	SourcePath      string `json:"sourcePath"`
	TargetServerID  int    `json:"targetServerId"`
	TargetPath      string `json:"targetPath"`
	Overwrite       bool   `json:"overwrite"`
}

// CopyResponse is the response for copying a file.
type CopyResponse struct {
	SourcePath string `json:"sourcePath"`
	TargetPath string `json:"targetPath"`
	Message    string `json:"message"`
}

// DownloadRequest is the request for downloading a file.
type DownloadRequest struct {
	Path string `json:"path"`
}

// UploadRequest is the request for uploading a file.
type UploadRequest struct {
	Path      string `json:"path"`
	Content   []byte `json:"content"`
	Mode      string `json:"mode"`
	Overwrite bool   `json:"overwrite"`
}

// UploadResponse is the response for uploading a file.
type UploadResponse struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Message string `json:"message"`
}

// SearchRequest is the request for searching files.
type SearchRequest struct {
	Path      string `json:"path"`
	Pattern   string `json:"pattern"`
	Recursive bool   `json:"recursive"`
	FileType  string `json:"fileType"` // "file", "dir", "all"
}

// SearchResponse is the response for searching files.
type SearchResponse struct {
	Path    string     `json:"path"`
	Results []FileInfo `json:"results"`
	Total   int        `json:"total"`
}

// StatRequest is the request for getting file stats.
type StatRequest struct {
	Path string `json:"path"`
}

// StatResponse is the response for getting file stats.
type StatResponse struct {
	FileInfo
	Exists bool `json:"exists"`
}

// Error responses
var (
	ErrFileNotFound      = "file not found"
	ErrDirectoryNotFound = "directory not found"
	ErrPermissionDenied  = "permission denied"
	ErrFileExists        = "file already exists"
	ErrInvalidPath       = "invalid path"
	ErrFileTooLarge      = "file too large"
	ErrBinaryFile        = "binary file cannot be displayed"
)
