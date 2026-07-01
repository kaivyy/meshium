package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	cronmod "meshium/internal/mod/cron"
	"meshium/internal/mod/file"
	"meshium/internal/shared"
)

// FileHandler handles HTTP requests for file operations.
type FileHandler struct {
	service *file.Service
	cron    *CronHandler
}

// NewFileHandler creates a new file handler.
func NewFileHandler(service *file.Service) *FileHandler {
	h := &FileHandler{service: service}
	if service != nil {
		if pool := service.SSHPool(); pool != nil {
			if cronSvc := cronmod.NewService(service.ServerRepo(), pool, service.AuthSvc(), service.KnownHosts()); cronSvc != nil {
				h.cron = NewCronHandler(cronSvc)
			}
		}
	}
	return h
}

// RegisterRoutes registers file routes on the given mux.
func (h *FileHandler) RegisterRoutes(mux *http.ServeMux) {
	// List directory
	mux.HandleFunc("GET /api/servers/{id}/files", h.handleList)

	// Read file content
	mux.HandleFunc("GET /api/servers/{id}/files/content", h.handleGetContent)

	// Download file
	mux.HandleFunc("GET /api/servers/{id}/files/download", h.handleDownload)

	// Upload file
	mux.HandleFunc("POST /api/servers/{id}/files/upload", h.handleUpload)

	// Write file
	mux.HandleFunc("POST /api/servers/{id}/files", h.handleWrite)

	// Delete file
	mux.HandleFunc("DELETE /api/servers/{id}/files", h.handleDelete)

	// Rename/move file
	mux.HandleFunc("PUT /api/servers/{id}/files", h.handleRename)

	// Create directory
	mux.HandleFunc("POST /api/servers/{id}/files/mkdir", h.handleMkdir)

	// Get file stats
	mux.HandleFunc("GET /api/servers/{id}/files/stat", h.handleStat)

	if h.cron != nil {
		h.cron.RegisterRoutes(mux)
	}

	if h.service != nil {
		if updatesSvc := h.service.SysUpdateService(); updatesSvc != nil {
			NewSysUpdateHandler(updatesSvc).RegisterRoutes(mux)
		}
	}

	// Register other route groups that are initialized alongside the file handler.
	registerDockerRoutes(mux)
}

// handleList handles GET /api/servers/{id}/files?path=/path/to/dir
func (h *FileHandler) handleList(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}

	showHidden := r.URL.Query().Get("hidden") == "true"

	files, err := h.service.ListDirectory(r.Context(), serverID, path, showHidden)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, file.ListDirectoryResponse{
		Path:  path,
		Files: files,
		Total: len(files),
	})
}

// handleGetContent handles GET /api/servers/{id}/files/content?path=/path/to/file
func (h *FileHandler) handleGetContent(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "path is required", "BAD_REQUEST")
		return
	}

	maxSizeStr := r.URL.Query().Get("maxSize")
	var maxSize int64 = 0
	if maxSizeStr != "" {
		maxSize, _ = strconv.ParseInt(maxSizeStr, 10, 64)
	}

	resp, err := h.service.ReadFile(r.Context(), serverID, path, maxSize)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, resp)
}

// handleDownload handles GET /api/servers/{id}/files/download?path=/path/to/file
func (h *FileHandler) handleDownload(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "path is required", "BAD_REQUEST")
		return
	}

	content, filename, err := h.service.DownloadFile(r.Context(), serverID, path)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	// Set headers for download
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Write(content)
}

// handleUpload handles POST /api/servers/{id}/files/upload
func (h *FileHandler) handleUpload(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	// Parse multipart form
	maxSize := int64(100 * 1024 * 1024) // 100MB max
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	err = r.ParseMultipartForm(maxSize)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "failed to parse form: "+err.Error(), "BAD_REQUEST")
		return
	}

	path := r.FormValue("path")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "path is required", "BAD_REQUEST")
		return
	}

	mode := r.FormValue("mode")
	overwrite := r.FormValue("overwrite") == "true"

	// Get file from form
	f, header, err := r.FormFile("file")
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "file is required", "BAD_REQUEST")
		return
	}
	defer f.Close()

	// Read file content
	content, err := io.ReadAll(f)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to read file", "INTERNAL_ERROR")
		return
	}

	// Write file
	writeReq := file.WriteFileRequest{
		Path:      path,
		Content:   string(content),
		Mode:      mode,
		Overwrite: overwrite,
	}

	err = h.service.WriteFile(r.Context(), serverID, writeReq)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			shared.WriteError(w, http.StatusConflict, err.Error(), "CONFLICT")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, file.UploadResponse{
		Path:    path,
		Size:    int64(len(content)),
		Message: fmt.Sprintf("uploaded %s (%d bytes)", header.Filename, len(content)),
	})
}

// handleWrite handles POST /api/servers/{id}/files (write content to file)
func (h *FileHandler) handleWrite(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	var req file.WriteFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	if req.Path == "" {
		shared.WriteError(w, http.StatusBadRequest, "path is required", "BAD_REQUEST")
		return
	}

	err = h.service.WriteFile(r.Context(), serverID, req)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			shared.WriteError(w, http.StatusConflict, err.Error(), "CONFLICT")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, file.WriteFileResponse{
		Path:    req.Path,
		Size:    int64(len(req.Content)),
		Message: "file written successfully",
	})
}

// handleDelete handles DELETE /api/servers/{id}/files?path=/path/to/file&recursive=true
func (h *FileHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "path is required", "BAD_REQUEST")
		return
	}

	recursive := r.URL.Query().Get("recursive") == "true"

	req := file.DeleteRequest{
		Path:      path,
		Recursive: recursive,
	}

	err = h.service.Delete(r.Context(), serverID, req)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, file.DeleteResponse{
		Path:    path,
		Message: "deleted successfully",
	})
}

// handleRename handles PUT /api/servers/{id}/files
func (h *FileHandler) handleRename(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	var req file.RenameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	if req.OldPath == "" || req.NewPath == "" {
		shared.WriteError(w, http.StatusBadRequest, "oldPath and newPath are required", "BAD_REQUEST")
		return
	}

	err = h.service.Rename(r.Context(), serverID, req)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, file.RenameResponse{
		OldPath: req.OldPath,
		NewPath: req.NewPath,
		Message: "renamed successfully",
	})
}

// handleMkdir handles POST /api/servers/{id}/files/mkdir
func (h *FileHandler) handleMkdir(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	var req file.MkdirRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	if req.Path == "" {
		shared.WriteError(w, http.StatusBadRequest, "path is required", "BAD_REQUEST")
		return
	}

	err = h.service.Mkdir(r.Context(), serverID, req)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, file.MkdirResponse{
		Path:    req.Path,
		Message: "directory created successfully",
	})
}

// handleStat handles GET /api/servers/{id}/files/stat?path=/path/to/file
func (h *FileHandler) handleStat(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "path is required", "BAD_REQUEST")
		return
	}

	info, err := h.service.Stat(r.Context(), serverID, path)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, info)
}
