package main

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	apiPrefix        = "/api"
	filesPrefix      = "/files"
	wsPrefix         = "/ws"
	defaultAPIPort   = "8080"
	version          = "1.0.0"
	maxDocUpdateSize = 2 * 1024 * 1024 // 2MB
)

var (
	latexRoot = envOr("LATEX_FILES_DIR", "./latex_files")
	upgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	projectState = newProjectState()
)

type Project struct {
	ProjectID string    `json:"projectId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Engine    string    `json:"engine,omitempty"`
	EntryFile string    `json:"entryFile,omitempty"`
}

type FileInfo struct {
	Path      string    `json:"path"`
	Type      string    `json:"type"` // "file" or "dir"
	Size      int64     `json:"size,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

type ErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type WSCommon struct {
	Type      string `json:"type"`
	ProjectID string `json:"projectId"`
	TS        string `json:"ts"`
	Revision  string `json:"revision,omitempty"`
}

type WSDocUpdate struct {
	WSCommon
	EntryFile string        `json:"entryFile"`
	Content   string        `json:"content"`
	Cursor    *CursorCursor `json:"cursor,omitempty"`
}
type CursorCursor struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

type WSRequestCompile struct {
	WSCommon
	Reason    string `json:"reason"`
	Engine    string `json:"engine,omitempty"`
	EntryFile string `json:"entryFile,omitempty"`
}

type WSSave struct {
	WSCommon
	Files   []PutFile `json:"files"`
	Message string    `json:"message,omitempty"`
}

type WSPing struct {
	Type      string `json:"type"`
	ProjectID string `json:"projectId"`
	TS        string `json:"ts"`
}

type WSAck struct {
	Type      string            `json:"type"` // "ack"
	ProjectID string            `json:"projectId"`
	TS        string            `json:"ts"`
	Op        string            `json:"op"`
	Revision  string            `json:"revision,omitempty"`
	Error     *map[string]any   `json:"error,omitempty"`
	Extras    map[string]string `json:"-"`
}

type CompileQueued struct {
	Type      string `json:"type"` // "compileQueued"
	ProjectID string `json:"projectId"`
	TS        string `json:"ts"`
	JobID     string `json:"jobId"`
	Revision  string `json:"revision"`
}
type CompileStarted struct {
	Type      string `json:"type"` // "compileStarted"
	ProjectID string `json:"projectId"`
	TS        string `json:"ts"`
	JobID     string `json:"jobId"`
	Revision  string `json:"revision"`
	StartedAt string `json:"startedAt"`
}
type CompileProgress struct {
	Type      string `json:"type"` // "compileProgress"
	ProjectID string `json:"projectId"`
	TS        string `json:"ts"`
	JobID     string `json:"jobId"`
	Revision  string `json:"revision"`
	Pct       *int   `json:"pct,omitempty"`
	Message   string `json:"message,omitempty"`
	LogTail   string `json:"logTail,omitempty"`
}
type CompileSucceeded struct {
	Type       string `json:"type"` // "compileSucceeded"
	ProjectID  string `json:"projectId"`
	TS         string `json:"ts"`
	JobID      string `json:"jobId"`
	Revision   string `json:"revision"`
	PDFURL     string `json:"pdfUrl"`
	Duration   int64  `json:"durationMs"`
	FinishedAt string `json:"finishedAt"`
	OutputPath string `json:"outputPath,omitempty"`
}
type CompileFailed struct {
	Type       string                   `json:"type"` // "compileFailed"
	ProjectID  string                   `json:"projectId"`
	TS         string                   `json:"ts"`
	JobID      string                   `json:"jobId"`
	Revision   string                   `json:"revision"`
	Errors     []map[string]interface{} `json:"errors,omitempty"`
	LogURL     string                   `json:"logUrl,omitempty"`
	Duration   int64                    `json:"durationMs"`
	FinishedAt string                   `json:"finishedAt"`
	Error      string                   `json:"error,omitempty"`
}
type CompileCanceled struct {
	Type               string `json:"type"` // "compileCanceled"
	ProjectID          string `json:"projectId"`
	TS                 string `json:"ts"`
	JobID              string `json:"jobId"`
	SupersededRevision string `json:"supersededByRevision"`
}
type WSPong struct {
	Type      string `json:"type"` // "pong"
	ProjectID string `json:"projectId"`
	TS        string `json:"ts"`
}

type PutFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type PutFilesBody struct {
	Files []PutFile `json:"files"`
}

type SavedFile struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

type SavedFilesResp struct {
	Saved []SavedFile `json:"saved"`
}

type CompileRequest struct {
	Reason    string `json:"reason"`
	Revision  string `json:"revision"`
	Engine    string `json:"engine,omitempty"`
	EntryFile string `json:"entryFile,omitempty"`
}

type CompileAccepted struct {
	JobID    string `json:"jobId"`
	Revision string `json:"revision"`
}

type CancelRequest struct {
	JobID    string `json:"jobId,omitempty"`
	Revision string `json:"revision,omitempty"`
}

type ProjectListItem struct {
	ProjectID string    `json:"projectId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ProjectsCreateBody struct {
	Name     string `json:"name"`
	Template string `json:"template"`
	Engine   string `json:"engine"`
}

type ProjectDetail struct {
	ProjectID string     `json:"projectId"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	Engine    string     `json:"engine"`
	EntryFile string     `json:"entryFile"`
	Files     []FileInfo `json:"files"`
}

// In-memory project registry and latest revision
type projectRegistry struct {
	mu        sync.RWMutex
	projects  map[string]*Project
	revisions map[string]string // latestRevision per project
	buffers   map[string]map[string]string
}

func newProjectState() *projectRegistry {
	return &projectRegistry{
		projects:  map[string]*Project{},
		revisions: map[string]string{},
		buffers:   map[string]map[string]string{},
	}
}

func (pr *projectRegistry) setLatestRevision(projectID, rev string) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.revisions[projectID] = rev
}

func (pr *projectRegistry) getLatestRevision(projectID string) string {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	return pr.revisions[projectID]
}

func (pr *projectRegistry) setBuffer(projectID, entry, content string) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if _, ok := pr.buffers[projectID]; !ok {
		pr.buffers[projectID] = map[string]string{}
	}
	pr.buffers[projectID][entry] = content
}

func main() {
	log.Printf("api-service starting on :%s, LATEX_FILES_DIR=%s", defaultAPIPort, latexRoot)

	// Ensure base directory
	if err := os.MkdirAll(latexRoot, 0o755); err != nil {
		log.Fatalf("failed to ensure latex root: %v", err)
	}

	mux := http.NewServeMux()

	// REST: Health and Version
	mux.HandleFunc(apiPrefix+"/health", handleHealth)
	mux.HandleFunc(apiPrefix+"/version", handleVersion)

	// REST: Projects
	mux.HandleFunc(apiPrefix+"/projects", routeProjects)
	mux.HandleFunc(apiPrefix+"/projects/", routeProjectByID)

	// Static files under /files/*
	mux.HandleFunc(filesPrefix+"/", handleFiles)

	// WebSocket
	mux.HandleFunc(wsPrefix+"/projects/", handleWSProjects)

	// Also provide legacy /health for current frontend default
	mux.HandleFunc("/health", handleHealth)

	addr := ":" + defaultAPIPort
	if err := http.ListenAndServe(addr, loggingMiddleware(mux)); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok")
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, http.StatusOK, map[string]string{"api": version})
}

// --- helpers added below ---

// writeJSON writes JSON with status and sets headers.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func getProject(projectID string) *Project {
	projectState.mu.RLock()
	defer projectState.mu.RUnlock()
	return projectState.projects[projectID]
}

func defaultTemplate(t string) string {
	switch strings.ToLower(t) {
	case "book":
		return `\documentclass{book}
\begin{document}
\chapter{Title}
Hello, book.
\end{document}
`
	case "empty":
		return `\documentclass{article}
\begin{document}
\end{document}
`
	default:
		return `\documentclass{article}
\begin{document}
Hello, LaTeX.
\end{document}
`
	}
}

// genToken creates a short opaque token suitable for revisions when client omits.
func genToken() string {
	return uuid()
}

// Projects routing
func routeProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		handleCreateProject(w, r)
	case http.MethodGet:
		handleListProjects(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, ErrorBody{"method not allowed", "method_not_allowed"})
	}
}

func routeProjectByID(w http.ResponseWriter, r *http.Request) {
	// /api/projects/{id}/...
	rest := strings.TrimPrefix(r.URL.Path, apiPrefix+"/projects/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"missing projectId", "invalid_request"})
		return
	}
	projectID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			handleGetProject(w, r, projectID)
		case http.MethodDelete:
			handleDeleteProject(w, r, projectID)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, ErrorBody{"method not allowed", "method_not_allowed"})
		}
		return
	}
	switch parts[1] {
	case "tree":
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorBody{"method not allowed", "method_not_allowed"})
			return
		}
		handleProjectTree(w, r, projectID)
	case "files":
		switch r.Method {
		case http.MethodGet:
			handleGetFile(w, r, projectID)
		case http.MethodPut:
			handlePutFiles(w, r, projectID)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, ErrorBody{"method not allowed", "method_not_allowed"})
		}
	case "compile":
		if len(parts) == 2 {
			if r.Method != http.MethodPost {
				writeJSON(w, http.StatusMethodNotAllowed, ErrorBody{"method not allowed", "method_not_allowed"})
				return
			}
			handleCompile(w, r, projectID)
		} else if len(parts) == 3 && parts[2] == "cancel" {
			if r.Method != http.MethodPost {
				writeJSON(w, http.StatusMethodNotAllowed, ErrorBody{"method not allowed", "method_not_allowed"})
				return
			}
			handleCompileCancel(w, r, projectID)
		} else {
			writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
		}
	case "download":
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorBody{"method not allowed", "method_not_allowed"})
			return
		}
		handleProjectDownload(w, r, projectID)
	default:
		writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
	}
}

// Stream a ZIP of the entire project directory
func handleProjectDownload(w http.ResponseWriter, r *http.Request, projectID string) {
	p := getProject(projectID)
	if p == nil {
		writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
		return
	}
	root := projectDir(projectID)
	// Set headers for zip download
	w.Header().Set("Content-Type", "application/zip")
	// sanitize name for filename
	fname := projectID + ".zip"
	if p.Name != "" {
		safeName := strings.ReplaceAll(p.Name, " ", "_")
		safeName = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				return r
			}
			return '-'
		}, safeName)
		fname = safeName + "-" + projectID + ".zip"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fname))

	zw := zip.NewWriter(w)
	defer zw.Close()
	// Walk project and add files (skip transient compile artifacts)
	filepath.WalkDir(root, func(pth string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// skip root
		if pth == root {
			return nil
		}
		rel, _ := filepath.Rel(root, pth)
		rel = filepath.ToSlash(rel)
		// Skip transient dirs and files
		if rel == "compile/working" || strings.HasPrefix(rel, "compile/working/") {
			return nil
		}
		if rel == "compile/queue" || strings.HasPrefix(rel, "compile/queue/") {
			return nil
		}
		if rel == "compile/status" || strings.HasPrefix(rel, "compile/status/") {
			return nil
		}
		if strings.HasSuffix(rel, ".cancel") {
			return nil
		}
		if d.IsDir() {
			// create directory entry to preserve structure (optional)
			_, _ = zw.Create(rel + "/")
			return nil
		}
		// add file
		f, err := os.Open(pth)
		if err != nil {
			return nil
		}
		defer f.Close()
		hdr, err := zip.FileInfoHeader(mustStat(f))
		if err != nil {
			return nil
		}
		hdr.Name = rel
		hdr.Method = zip.Deflate
		wtr, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil
		}
		_, _ = io.Copy(wtr, f)
		return nil
	})
}

func handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body ProjectsCreateBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid json", "bad_json"})
		return
	}
	if body.Name == "" {
		body.Name = "Untitled Project"
	}
	if body.Template == "" {
		body.Template = "article"
	}
	if body.Engine == "" {
		body.Engine = "pdflatex"
	}
	id := uuid()
	now := time.Now().UTC()
	p := &Project{
		ProjectID: id,
		Name:      body.Name,
		CreatedAt: now,
		UpdatedAt: now,
		Engine:    body.Engine,
		EntryFile: "main.tex",
	}
	// create directory
	root := projectDir(id)
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorBody{"failed to create project", "internal_error"})
		return
	}
	compileDirs := []string{
		filepath.Join(root, "compile", "queue"),
		filepath.Join(root, "compile", "working"),
		filepath.Join(root, "compile", "logs"),
		filepath.Join(root, "compile", "status"),
	}
	for _, d := range compileDirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorBody{"failed to create compile dirs", "internal_error"})
			return
		}
	}
	seed := defaultTemplate(body.Template)
	if err := os.WriteFile(filepath.Join(root, "main.tex"), []byte(seed), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorBody{"failed to write main.tex", "internal_error"})
		return
	}
	// register
	projectState.mu.Lock()
	projectState.projects[id] = p
	projectState.mu.Unlock()

	resp := map[string]any{
		"projectId": id,
		// include short key expected by some frontends
		"id":        id,
		"name":      p.Name,
		"createdAt": p.CreatedAt.Format(time.RFC3339),
		"updatedAt": p.UpdatedAt.Format(time.RFC3339),
	}
	writeJSON(w, http.StatusCreated, resp)
}

func handleListProjects(w http.ResponseWriter, r *http.Request) {
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := clamp(parseIntDefault(r.URL.Query().Get("pageSize"), 20), 1, 100)
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	projectState.mu.RLock()
	defer projectState.mu.RUnlock()
	var list []ProjectListItem
	for _, p := range projectState.projects {
		if search != "" && !strings.Contains(strings.ToLower(p.Name), search) {
			continue
		}
		list = append(list, ProjectListItem{
			ProjectID: p.ProjectID,
			Name:      p.Name,
			CreatedAt: p.CreatedAt,
			UpdatedAt: p.UpdatedAt,
		})
	}
	start := (page - 1) * pageSize
	if start > len(list) {
		start = len(list)
	}
	end := start + pageSize
	if end > len(list) {
		end = len(list)
	}
	writeJSON(w, http.StatusOK, list[start:end])
}

func handleGetProject(w http.ResponseWriter, r *http.Request, projectID string) {
	p := getProject(projectID)
	if p == nil {
		writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
		return
	}
	files := []FileInfo{}
	root := projectDir(projectID)
	filepath.WalkDir(root, func(pth string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if pth == root {
			return nil
		}
		rel, _ := filepath.Rel(root, pth)
		if rel == "." {
			return nil
		}
		fi, _ := d.Info()
		info := FileInfo{Path: filepath.ToSlash(rel)}
		if d.IsDir() {
			info.Type = "dir"
		} else {
			info.Type = "file"
			if fi != nil {
				info.Size = fi.Size()
				info.UpdatedAt = fi.ModTime().UTC()
			}
		}
		files = append(files, info)
		return nil
	})
	resp := ProjectDetail{
		ProjectID: p.ProjectID,
		Name:      p.Name,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
		Engine:    coalesce(p.Engine, "pdflatex"),
		EntryFile: coalesce(p.EntryFile, "main.tex"),
		Files:     files,
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleDeleteProject(w http.ResponseWriter, r *http.Request, projectID string) {
	if getProject(projectID) == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// simple delete: remove directory
	if err := os.RemoveAll(projectDir(projectID)); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorBody{"delete failed", "internal_error"})
		return
	}
	projectState.mu.Lock()
	delete(projectState.projects, projectID)
	delete(projectState.revisions, projectID)
	delete(projectState.buffers, projectID)
	projectState.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func handleProjectTree(w http.ResponseWriter, r *http.Request, projectID string) {
	if getProject(projectID) == nil {
		writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
		return
	}
	root := projectDir(projectID)
	entries := []FileInfo{}
	err := filepath.WalkDir(root, func(pth string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if pth == root {
			return nil
		}
		rel, _ := filepath.Rel(root, pth)
		if rel == "." {
			return nil
		}
		fi, _ := d.Info()
		item := FileInfo{
			Path: filepath.ToSlash(rel),
		}
		if d.IsDir() {
			item.Type = "dir"
		} else {
			item.Type = "file"
			if fi != nil {
				item.Size = fi.Size()
			}
		}
		entries = append(entries, item)
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorBody{"failed to read tree", "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func handleGetFile(w http.ResponseWriter, r *http.Request, projectID string) {
	if getProject(projectID) == nil {
		writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
		return
	}
	pth := r.URL.Query().Get("path")
	if pth == "" {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"missing path", "invalid_request"})
		return
	}
	full, ok := safeJoin(projectDir(projectID), pth)
	if !ok {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid path", "invalid_path"})
		return
	}
	b, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorBody{"read failed", "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    filepath.ToSlash(pth),
		"content": string(b),
	})
}

func handlePutFiles(w http.ResponseWriter, r *http.Request, projectID string) {
	if getProject(projectID) == nil {
		writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
		return
	}
	var body PutFilesBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 10<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid json", "bad_json"})
		return
	}
	if len(body.Files) == 0 {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"no files", "invalid_request"})
		return
	}
	root := projectDir(projectID)
	var saved []SavedFile
	for _, f := range body.Files {
		full, ok := safeJoin(root, f.Path)
		if !ok {
			writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid path", "invalid_path"})
			return
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorBody{"mkdir failed", "internal_error"})
			return
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorBody{"write failed", "internal_error"})
			return
		}
		saved = append(saved, SavedFile{Path: filepath.ToSlash(f.Path), Bytes: len(f.Content)})
	}
	writeJSON(w, http.StatusOK, SavedFilesResp{Saved: saved})
}

func handleCompile(w http.ResponseWriter, r *http.Request, projectID string) {
	if getProject(projectID) == nil {
		writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
		return
	}
	var body CompileRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid json", "bad_json"})
		return
	}
	rev := body.Revision
	if rev == "" {
		rev = projectState.getLatestRevision(projectID)
	}
	latest := projectState.getLatestRevision(projectID)
	// accept even if rev != latest; the WS path guards enqueue, REST says enqueue for provided or current revision
	if rev == "" {
		rev = latest
	}
	jobID, err := enqueueJob(projectID, coalesce(body.EntryFile, "main.tex"), coalesce(body.Engine, "pdflatex"), rev, "rest:"+r.RemoteAddr)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorBody{"enqueue failed", "internal_error"})
		return
	}
	writeJSON(w, http.StatusAccepted, CompileAccepted{JobID: jobID, Revision: rev})
}

func handleCompileCancel(w http.ResponseWriter, r *http.Request, projectID string) {
	if getProject(projectID) == nil {
		writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
		return
	}
	var body CancelRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid json", "bad_json"})
		return
	}
	ok := false
	if body.JobID != "" {
		ok = writeCancelFile(projectID, body.JobID) == nil
	} else if body.Revision != "" {
		// cancel any job older than revision => this minimal impl: write global cancel marker by scanning status dir (omitted)
		ok = true
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"canceled": ok})
}

// Static file server for /files/{projectId}/...
func handleFiles(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, filesPrefix+"/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	projectID := parts[0]
	rel := parts[1]
	root := projectDir(projectID)
	full, ok := safeJoin(root, rel)
	if !ok {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid path", "invalid_path"})
		return
	}
	// set cache control as no-store
	w.Header().Set("Cache-Control", "no-store")
	// set content type via extension
	if ct := mime.TypeByExtension(filepath.Ext(full)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeFile(w, r, full)
}

// WebSocket handler: /ws/projects/{projectId}?token=...
func handleWSProjects(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, wsPrefix+"/projects/") {
		http.NotFound(w, r)
		return
	}
	projectID := strings.TrimPrefix(r.URL.Path, wsPrefix+"/projects/")
	if i := strings.Index(projectID, "/"); i >= 0 {
		projectID = projectID[:i]
	}
	if projectID == "" {
		http.Error(w, "missing projectId", http.StatusBadRequest)
		return
	}
	// Echo subprotocol if provided
	subproto := ""
	for _, p := range websocket.Subprotocols(r) {
		if p == "live-latex-v1" {
			subproto = p
			break
		}
	}
	up := upgrader
	up.Subprotocols = nil // Upgrader decides based on CheckOrigin; we manually set response protocol via header
	if subproto != "" {
		w.Header().Set("Sec-WebSocket-Protocol", subproto)
	}
	c, err := up.Upgrade(w, r, w.Header())
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// token accepted but not validated (future-proof)
	_ = r.URL.Query().Get("token")

	// Listen loop
	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) && !errors.Is(err, io.EOF) {
				log.Printf("ws read error: %v", err)
			}
			return
		}
		// generic decode for flexible client payloads
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			_ = sendAck(c, projectID, "unknown", nil, map[string]any{"message": "invalid json", "code": "invalid_message"})
			continue
		}
		t, _ := payload["type"].(string)
		switch t {
		case "docUpdate":
			// extract fields allowing both path/entryFile and number/string revision
			content, _ := payload["content"].(string)
			if len(content) > maxDocUpdateSize {
				_ = sendAck(c, projectID, "docUpdate", nil, map[string]any{"message": "too large", "code": "size_limit_exceeded"})
				_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseMessageTooBig, "payload too large"), time.Now().Add(time.Second))
				continue
			}
			// determine entry file from either path or entryFile
			entry := ""
			if pth, ok := payload["path"].(string); ok && pth != "" {
				entry = pth
			}
			if ef, ok := payload["entryFile"].(string); ok && ef != "" {
				entry = ef
			}
			if entry == "" {
				entry = "main.tex"
			}
			// revision: accept number or string
			var revStr string
			var ackRev any
			switch rv := payload["revision"].(type) {
			case float64:
				// JSON numbers decode to float64
				revStr = strconv.FormatInt(int64(rv), 10)
				ackRev = rv
			case string:
				revStr = rv
				ackRev = rv
			default:
				revStr = genToken()
				ackRev = revStr
			}
			projectState.setLatestRevision(projectID, revStr)
			projectState.setBuffer(projectID, entry, content)
			// write latest.token
			if err := os.WriteFile(filepath.Join(projectDir(projectID), "compile", "latest.token"), []byte(revStr), 0o644); err != nil {
				log.Printf("write latest.token error: %v", err)
			}
			_ = sendAck(c, projectID, "docUpdate", ackRev, nil)
		case "requestCompile":
			// allow flexible fields; always compile the latest revision to keep preview live
			engine := "pdflatex"
			if e, ok := payload["engine"].(string); ok && e != "" {
				engine = e
			}
			entry := "main.tex"
			if pth, ok := payload["path"].(string); ok && pth != "" {
				entry = pth
			}
			if ef, ok := payload["entryFile"].(string); ok && ef != "" {
				entry = ef
			}
			latest := projectState.getLatestRevision(projectID)
			// prefer provided revision if it equals latest; otherwise force latest
			var ackRev any
			switch rv := payload["revision"].(type) {
			case float64:
				if strconv.FormatInt(int64(rv), 10) == latest {
					ackRev = rv
				} else {
					ackRev = latest
				}
			case string:
				if rv == latest {
					ackRev = rv
				} else {
					ackRev = latest
				}
			default:
				ackRev = latest
			}
			revStr := latest
			jobID, err := enqueueJob(projectID, entry, engine, revStr, "ws")
			if err != nil {
				_ = sendAck(c, projectID, "requestCompile", ackRev, map[string]any{"message": "enqueue failed", "code": "internal_error"})
				continue
			}
			_ = sendAck(c, projectID, "requestCompile", ackRev, nil)
			now := time.Now().UTC().Format(time.RFC3339)
			_ = c.WriteJSON(CompileQueued{Type: "compileQueued", ProjectID: projectID, TS: now, JobID: jobID, Revision: revStr})
			// kick a lightweight status watcher to emit started/progress/succeeded if status files appear
			go watchJobStatus(ctx, c, projectID, jobID, revStr)
		case "save":
			// support save via WS with { files: [{ path, content }] }
			files, ok := payload["files"].([]any)
			if !ok || len(files) == 0 {
				_ = sendAck(c, projectID, "save", nil, map[string]any{"message": "no files", "code": "invalid_message"})
				continue
			}
			for _, it := range files {
				fm, ok := it.(map[string]any)
				if !ok {
					continue
				}
				pth, _ := fm["path"].(string)
				cnt, _ := fm["content"].(string)
				full, ok := safeJoin(projectDir(projectID), pth)
				if !ok {
					_ = sendAck(c, projectID, "save", nil, map[string]any{"message": "invalid path", "code": "invalid_path"})
					continue
				}
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					_ = sendAck(c, projectID, "save", nil, map[string]any{"message": "mkdir failed", "code": "internal_error"})
					continue
				}
				if err := os.WriteFile(full, []byte(cnt), 0o644); err != nil {
					_ = sendAck(c, projectID, "save", nil, map[string]any{"message": "write failed", "code": "internal_error"})
					continue
				}
			}
			_ = sendAck(c, projectID, "save", projectState.getLatestRevision(projectID), nil)
		case "ping":
			_ = c.WriteJSON(WSPong{Type: "pong", ProjectID: projectID, TS: time.Now().UTC().Format(time.RFC3339)})
		default:
			_ = sendAck(c, projectID, t, nil, map[string]any{"message": "unknown type", "code": "invalid_message"})
		}
	}
}

func watchJobStatus(ctx context.Context, c *websocket.Conn, projectID, jobID, rev string) {
	statusPath := filepath.Join(projectDir(projectID), "compile", "status", jobID+".json")
	logPath := filepath.Join(projectDir(projectID), "compile", "logs", jobID+".txt")
	startedEmitted := false
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// read status json if exists
			b, err := os.ReadFile(statusPath)
			if err != nil {
				continue
			}
			var s struct {
				JobID     string `json:"jobId"`
				ProjectID string `json:"projectId"`
				State     string `json:"state"`
				Revision  string `json:"revision"`
				StartedAt string `json:"startedAt"`
				Finished  string `json:"finishedAt"`
				Duration  int64  `json:"durationMs"`
			}
			if json.Unmarshal(b, &s) != nil {
				continue
			}
			now := time.Now().UTC().Format(time.RFC3339)
			if !startedEmitted && (s.State == "running" || s.State == "success" || s.State == "failed") {
				_ = c.WriteJSON(CompileStarted{Type: "compileStarted", ProjectID: projectID, TS: now, JobID: jobID, Revision: rev, StartedAt: s.StartedAt})
				startedEmitted = true
			}
			// tail small chunk of log
			tail := readTail(logPath, 4096)
			if tail != "" && s.State == "running" {
				_ = c.WriteJSON(CompileProgress{Type: "compileProgress", ProjectID: projectID, TS: now, JobID: jobID, Revision: rev, LogTail: tail, Message: tail})
			}
			switch s.State {
			case "success":
				_ = c.WriteJSON(CompileSucceeded{
					Type:       "compileSucceeded",
					ProjectID:  projectID,
					TS:         now,
					JobID:      jobID,
					Revision:   rev,
					PDFURL:     fmt.Sprintf("/files/%s/output.pdf", projectID),
					Duration:   time.Since(start).Milliseconds(),
					FinishedAt: s.Finished,
					OutputPath: fmt.Sprintf("/files/%s/output.pdf", projectID),
				})
				return
			case "failed":
				_ = c.WriteJSON(CompileFailed{
					Type:       "compileFailed",
					ProjectID:  projectID,
					TS:         now,
					JobID:      jobID,
					Revision:   rev,
					LogURL:     fmt.Sprintf("/files/%s/compile/logs/%s.txt", projectID, jobID),
					Duration:   time.Since(start).Milliseconds(),
					FinishedAt: s.Finished,
					Error:      tail,
				})
				return
			case "canceled", "superseded":
				_ = c.WriteJSON(CompileCanceled{
					Type:               "compileCanceled",
					ProjectID:          projectID,
					TS:                 now,
					JobID:              jobID,
					SupersededRevision: projectState.getLatestRevision(projectID),
				})
				return
			}
		}
	}
}

func enqueueJob(projectID, entryFile, engine, revision, requestor string) (string, error) {
	jobID := uuid()
	root := projectDir(projectID)
	job := map[string]any{
		"jobId":       jobID,
		"projectId":   projectID,
		"entryFile":   entryFile,
		"engine":      coalesce(engine, "pdflatex"),
		"options":     []string{"-interaction=nonstopmode", "-halt-on-error"},
		"cancelToken": revision,
		"createdAt":   time.Now().UTC().Format(time.RFC3339),
		"requestor":   requestor,
	}
	b, _ := json.MarshalIndent(job, "", "  ")
	qpath := filepath.Join(root, "compile", "queue", jobID+".json")
	if err := os.WriteFile(qpath, b, 0o644); err != nil {
		return "", err
	}
	// Start simulated compilation flow unless disabled via env
	if isSimulationEnabled() {
		go simulateCompilation(projectID, jobID, entryFile, engine, revision)
	}
	return jobID, nil
}

func writeCancelFile(projectID, jobID string) error {
	return os.WriteFile(filepath.Join(projectDir(projectID), "compile", jobID+".cancel"), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
}

// mustStat returns FileInfo or panics; only used inside zip stream where errors are ignored.
func mustStat(f *os.File) os.FileInfo {
	fi, err := f.Stat()
	if err != nil {
		panic(err)
	}
	return fi
}

// ----- Simulation helpers to make preview work without a real compiler -----

func isSimulationEnabled() bool {
	// Default disabled; enable only if SIMULATE_COMPILER is truthy
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SIMULATE_COMPILER")))
	return v == "1" || v == "true" || v == "on" || v == "yes"
}

func simulateCompilation(projectID, jobID, entryFile, engine, revision string) {
	root := projectDir(projectID)
	statusPath := filepath.Join(root, "compile", "status", jobID+".json")
	logPath := filepath.Join(root, "compile", "logs", jobID+".txt")
	started := time.Now().UTC()
	// mark running
	_ = writeStatusJSON(statusPath, map[string]any{
		"jobId":     jobID,
		"projectId": projectID,
		"state":     "running",
		"revision":  revision,
		"startedAt": started.Format(time.RFC3339),
	})
	_ = appendLog(logPath, "Simulated compiler starting...\n")
	// simulate reading entry file
	full, ok := safeJoin(root, coalesce(entryFile, "main.tex"))
	if !ok {
		_ = appendLog(logPath, "Invalid entry file path\n")
	}
	if b, err := os.ReadFile(full); err == nil {
		_ = appendLog(logPath, fmt.Sprintf("Read %s (%d bytes)\n", filepath.ToSlash(coalesce(entryFile, "main.tex")), len(b)))
	} else {
		_ = appendLog(logPath, "Entry file missing, continuing with placeholder PDF...\n")
	}
	// simulate work
	time.Sleep(400 * time.Millisecond)
	// write placeholder PDF only if a real output does not already exist
	outPath := filepath.Join(root, "output.pdf")
	if _, statErr := os.Stat(outPath); statErr == nil {
		_ = appendLog(logPath, "Detected existing output.pdf; preserving real output.\n")
	} else if err := writePlaceholderPDF(outPath); err != nil {
		_ = appendLog(logPath, "Failed to write PDF: "+err.Error()+"\n")
		finish := time.Now().UTC()
		_ = writeStatusJSON(statusPath, map[string]any{
			"jobId":      jobID,
			"projectId":  projectID,
			"state":      "failed",
			"revision":   revision,
			"startedAt":  started.Format(time.RFC3339),
			"finishedAt": finish.Format(time.RFC3339),
			"durationMs": finish.Sub(started).Milliseconds(),
		})
		return
	}
	_ = appendLog(logPath, "Wrote placeholder PDF output.pdf\n")
	finish := time.Now().UTC()
	_ = writeStatusJSON(statusPath, map[string]any{
		"jobId":      jobID,
		"projectId":  projectID,
		"state":      "success",
		"revision":   revision,
		"startedAt":  started.Format(time.RFC3339),
		"finishedAt": finish.Format(time.RFC3339),
		"durationMs": finish.Sub(started).Milliseconds(),
	})
}

func writeStatusJSON(path string, v map[string]any) error {
	b, _ := json.Marshal(v)
	return os.WriteFile(path, b, 0o644)
}

func appendLog(path, line string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

// Minimal valid PDF so the browser can render
func writePlaceholderPDF(dst string) error {
	// ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	content := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Count 1/Kids[3 0 R]>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>endobj\n4 0 obj<</Length 112>>stream\nBT /F1 20 Tf 72 720 Td (Placeholder PDF) Tj ET\nBT /F1 12 Tf 72 700 Td (Waiting for real LaTeX output...) Tj ET\nendstream endobj\n5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\nxref\n0 6\n0000000000 65535 f \n0000000010 00000 n \n0000000065 00000 n \n0000000122 00000 n \n0000000339 00000 n \n0000000557 00000 n \ntrailer<</Size 6/Root 1 0 R>>\nstartxref\n647\n%%EOF\n"
	return os.WriteFile(dst, []byte(content), 0o644)
}

func readTail(p string, max int64) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	var start int64 = 0
	if fi.Size() > max {
		start = fi.Size() - max
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	sb := strings.Builder{}
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadString('\n')
		sb.WriteString(line)
		if err != nil {
			break
		}
	}
	return sb.String()
}

func handleWSAckError(c *websocket.Conn, projectID, op string, msg string) {
	_ = sendAck(c, projectID, op, nil, map[string]any{"message": msg, "code": "invalid_message"})
}

func sendAck(c *websocket.Conn, projectID, op string, revision any, errObj map[string]any) error {
	ack := map[string]any{
		"type":      "ack",
		"projectId": projectID,
		"ts":        time.Now().UTC().Format(time.RFC3339),
		"op":        op,
	}
	if revision != nil {
		ack["revision"] = revision
	}
	if errObj != nil {
		ack["error"] = errObj
	}
	return c.WriteJSON(ack)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func projectDir(projectID string) string {
	return filepath.Join(latexRoot, projectID)
}

func safeJoin(root, requested string) (string, bool) {
	// Normalize to forward slashes, clean, and ensure relative (no leading slash)
	cleanRel := path.Clean(filepath.ToSlash(requested))
	// Remove any leading slash to keep it relative under root
	cleanRel = strings.TrimPrefix(cleanRel, "/")
	// Forbid path traversal
	if cleanRel == ".." || strings.HasPrefix(cleanRel, "../") || strings.Contains(cleanRel, "/../") {
		return "", false
	}
	full := filepath.Join(root, cleanRel)
	full = filepath.Clean(full)
	rootClean := filepath.Clean(root)
	if !strings.HasPrefix(full, rootClean+string(os.PathSeparator)) && full != rootClean {
		return "", false
	}
	return full, true
}

func parseIntDefault(s string, d int) int {
	if s == "" {
		return d
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return i
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func coalesce[T comparable](v T, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	// Set version 4
	b[6] = (b[6] & 0x0f) | 0x40
	// Set variant
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
