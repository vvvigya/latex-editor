package main

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/json"
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
	serverStart  = time.Now()
)

type Project struct {
	ProjectID    string    `json:"id"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Engine       string    `json:"engine,omitempty"`
	EntryFile    string    `json:"entryFile,omitempty"`
	LastModified time.Time `json:"lastModified"`
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
	Type      string         `json:"type"` // "ack"
	ProjectID string         `json:"projectId"`
	TS        string         `json:"ts"`
	Op        string         `json:"op"`
	Revision  any            `json:"revision,omitempty"`
	Error     map[string]any `json:"error,omitempty"`
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
	Message   string `json:"message,omitempty"`
}
type CompileSucceeded struct {
	Type       string `json:"type"` // "compileSucceeded"
	ProjectID  string `json:"projectId"`
	TS         string `json:"ts"`
	JobID      string `json:"jobId"`
	Revision   string `json:"revision"`
	OutputPath string `json:"outputPath,omitempty"`
	FinishedAt string `json:"finishedAt"`
}
type CompileFailed struct {
	Type       string `json:"type"` // "compileFailed"
	ProjectID  string `json:"projectId"`
	TS         string `json:"ts"`
	JobID      string `json:"jobId"`
	Revision   string `json:"revision"`
	Error      string `json:"error,omitempty"`
	FinishedAt string `json:"finishedAt"`
}
type CompileCanceled struct {
	Type      string `json:"type"` // "compileCanceled"
	ProjectID string `json:"projectId"`
	TS        string `json:"ts"`
	JobID     string `json:"jobId"`
	Revision  string `json:"revision"`
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

type ProjectsCreateBody struct {
	Name     string `json:"name"`
	Template string `json:"template"`
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

	if err := os.MkdirAll(latexRoot, 0o755); err != nil {
		log.Fatalf("failed to ensure latex root: %v", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc(apiPrefix+"/health", handleHealth)
	mux.HandleFunc(apiPrefix+"/version", handleVersion)
	mux.HandleFunc(apiPrefix+"/projects", routeProjects)
	mux.HandleFunc(apiPrefix+"/projects/import", handleImportProject)
	mux.HandleFunc(apiPrefix+"/projects/", routeProjectByID)

	mux.HandleFunc(filesPrefix+"/", handleFiles)
	mux.HandleFunc(wsPrefix+"/projects/", handleWSProjects)

	// Legacy health for frontend
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
	info := map[string]string{
		"api":    version,
		"uptime": time.Since(serverStart).String(),
	}
	writeJSON(w, http.StatusOK, info)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

func getProject(projectID string) *Project {
	projectState.mu.RLock()
	defer projectState.mu.RUnlock()
	return projectState.projects[projectID]
}

func defaultTemplate(t string) string {
	switch strings.ToLower(t) {
	case "report":
		return `\documentclass{report}
\begin{document}
\title{My Report}
\author{Author}
\maketitle
\chapter{Introduction}
This is the introduction.
\end{document}
`
	case "beamer":
		return `\documentclass{beamer}
\usetheme{Madrid}
\title{My Presentation}
\author{Author}
\date{\today}
\begin{document}
\frame{\titlepage}
\section{Introduction}
\begin{frame}{First Slide}
Hello, Beamer!
\end{frame}
\end{document}
`
	default:
		return `\documentclass{article}
\begin{document}
Hello, LaTeX!
\end{document}
`
	}
}

func genToken() string {
	return uuid()
}

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

func handleImportProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorBody{"method not allowed", "method_not_allowed"})
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB limit
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid form", "bad_request"})
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"missing file", "bad_request"})
		return
	}
	defer file.Close()

	projectName := strings.TrimSuffix(handler.Filename, filepath.Ext(handler.Filename))

	id := uuid()
	now := time.Now().UTC()
	p := &Project{
		ProjectID:    id,
		Name:         projectName,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastModified: now,
		Engine:       "pdflatex",
		EntryFile:    "main.tex",
	}

	root := projectDir(id)
	if err := os.MkdirAll(root, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorBody{"failed to create project directory", "internal_error"})
		return
	}

	zipReader, err := zip.NewReader(file, handler.Size)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid zip file", "bad_zip"})
		return
	}

	for _, f := range zipReader.File {
		fpath := filepath.Join(root, f.Name)

		if !strings.HasPrefix(fpath, filepath.Clean(root)+string(os.PathSeparator)) {
			writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid file path in zip", "bad_zip_path"})
			return
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorBody{"failed to create directory from zip", "internal_error"})
			return
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorBody{"failed to create file from zip", "internal_error"})
			return
		}

		rc, err := f.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorBody{"failed to open file in zip", "internal_error"})
			return
		}

		_, err = io.Copy(outFile, rc)

		outFile.Close()
		rc.Close()

		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorBody{"failed to write file from zip", "internal_error"})
			return
		}
	}

	createCompileDirs(root)

	projectState.mu.Lock()
	projectState.projects[id] = p
	projectState.mu.Unlock()

	writeJSON(w, http.StatusCreated, p)
}

func createCompileDirs(root string) {
	dirs := []string{"compile/queue", "compile/working", "compile/logs", "compile/status"}
	for _, d := range dirs {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
}

func routeProjectByID(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, apiPrefix+"/projects/"), "/")
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
		handleCompile(w, r, projectID)
	case "download":
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
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", p.Name+".zip"))

	zw := zip.NewWriter(w)
	defer zw.Close()

	filepath.WalkDir(root, func(pth string, d os.DirEntry, err error) error {
		if err != nil || pth == root {
			return nil
		}
		rel, _ := filepath.Rel(root, pth)
		// Skip compile artifacts
		if strings.HasPrefix(rel, "compile") {
			return nil
		}
		if d.IsDir() {
			_, err := zw.Create(rel + "/")
			return err
		}
		f, err := os.Open(pth)
		if err != nil {
			return nil
		}
		defer f.Close()
		info, _ := f.Stat()
		hdr, _ := zip.FileInfoHeader(info)
		hdr.Name = rel
		hdr.Method = zip.Deflate
		wtr, _ := zw.CreateHeader(hdr)
		_, _ = io.Copy(wtr, f)
		return nil
	})
}

func handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body ProjectsCreateBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid json", "bad_json"})
		return
	}
	if body.Name == "" {
		body.Name = "Untitled Project"
	}

	id := uuid()
	now := time.Now().UTC()
	p := &Project{
		ProjectID:    id,
		Name:         body.Name,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastModified: now,
		Engine:       "pdflatex",
		EntryFile:    "main.tex",
	}

	root := projectDir(id)
	os.MkdirAll(filepath.Join(root, "assets"), 0o755)
	createCompileDirs(root)

	seed := defaultTemplate(body.Template)
	os.WriteFile(filepath.Join(root, "main.tex"), []byte(seed), 0o644)

	projectState.mu.Lock()
	projectState.projects[id] = p
	projectState.mu.Unlock()

	writeJSON(w, http.StatusCreated, p)
}

func handleListProjects(w http.ResponseWriter, r *http.Request) {
	projectState.mu.RLock()
	defer projectState.mu.RUnlock()
	var list []*Project
	for _, p := range projectState.projects {
		list = append(list, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": list})
}

func handleGetProject(w http.ResponseWriter, r *http.Request, projectID string) {
	p := getProject(projectID)
	if p == nil {
		writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func handleDeleteProject(w http.ResponseWriter, r *http.Request, projectID string) {
	if getProject(projectID) == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	os.RemoveAll(projectDir(projectID))
	projectState.mu.Lock()
	delete(projectState.projects, projectID)
	delete(projectState.revisions, projectID)
	delete(projectState.buffers, projectID)
	projectState.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func handleGetFile(w http.ResponseWriter, r *http.Request, projectID string) {
	pth := r.URL.Query().Get("path")
	full, ok := safeJoin(projectDir(projectID), pth)
	if !ok {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid path", "invalid_path"})
		return
	}
	b, err := os.ReadFile(full)
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorBody{"file not found", "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": pth, "content": string(b)})
}

func handlePutFiles(w http.ResponseWriter, r *http.Request, projectID string) {
	var body PutFilesBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 10<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorBody{"invalid json", "bad_json"})
		return
	}
	root := projectDir(projectID)
	var saved []SavedFile
	for _, f := range body.Files {
		full, ok := safeJoin(root, f.Path)
		if !ok {
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(f.Content), 0o644)
		saved = append(saved, SavedFile{Path: f.Path, Bytes: len(f.Content)})
	}
	writeJSON(w, http.StatusOK, SavedFilesResp{Saved: saved})
}

func handleCompile(w http.ResponseWriter, r *http.Request, projectID string) {
	p := getProject(projectID)
	if p == nil {
		writeJSON(w, http.StatusNotFound, ErrorBody{"not found", "not_found"})
		return
	}
	rev := projectState.getLatestRevision(projectID)
	jobID, err := enqueueJob(projectID, p.EntryFile, p.Engine, rev, "rest")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorBody{"enqueue failed", "internal_error"})
		return
	}
	writeJSON(w, http.StatusAccepted, CompileAccepted{JobID: jobID, Revision: rev})
}

func handleFiles(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, filesPrefix+"/"), "/", 2)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	projectID, rel := parts[0], parts[1]
	full, ok := safeJoin(projectDir(projectID), rel)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if ct := mime.TypeByExtension(filepath.Ext(full)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeFile(w, r, full)
}

func handleWSProjects(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimPrefix(r.URL.Path, wsPrefix+"/projects/")
	if projectID == "" {
		http.Error(w, "missing projectId", http.StatusBadRequest)
		return
	}

	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}

		var payload map[string]any
		if json.Unmarshal(data, &payload) != nil {
			continue
		}
		t, _ := payload["type"].(string)

		switch t {
		case "docUpdate":
			content, _ := payload["content"].(string)
			entry, _ := payload["path"].(string)
			if entry == "" {
				entry = "main.tex"
			}
			var revStr string
			switch rv := payload["revision"].(type) {
			case float64:
				revStr = strconv.FormatInt(int64(rv), 10)
			case string:
				revStr = rv
			default:
				revStr = genToken()
			}
			projectState.setLatestRevision(projectID, revStr)
			projectState.setBuffer(projectID, entry, content)
			sendAck(c, projectID, "docUpdate", payload["revision"], nil)

		case "requestCompile":
			entry, _ := payload["path"].(string)
			if entry == "" {
				entry = "main.tex"
			}
			revStr := projectState.getLatestRevision(projectID)
			jobID, err := enqueueJob(projectID, entry, "pdflatex", revStr, "ws")
			if err != nil {
				sendAck(c, projectID, "requestCompile", payload["revision"], map[string]any{"message": "enqueue failed"})
				continue
			}
			sendAck(c, projectID, "requestCompile", payload["revision"], nil)
			now := time.Now().UTC().Format(time.RFC3339)
			c.WriteJSON(CompileQueued{Type: "compileQueued", ProjectID: projectID, TS: now, JobID: jobID, Revision: revStr})
			go watchJobStatus(ctx, c, projectID, jobID, revStr)

		case "ping":
			c.WriteJSON(WSPong{Type: "pong", ProjectID: projectID, TS: time.Now().UTC().Format(time.RFC3339)})
		}
	}
}

func watchJobStatus(ctx context.Context, c *websocket.Conn, projectID, jobID, rev string) {
	statusPath := filepath.Join(projectDir(projectID), "compile", "status", jobID+".json")
	logPath := filepath.Join(projectDir(projectID), "compile", "logs", jobID+".txt")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b, err := os.ReadFile(statusPath)
			if err != nil {
				continue
			}
			var s struct {
				State      string `json:"state"`
				StartedAt  string `json:"startedAt"`
				FinishedAt string `json:"finishedAt"`
			}
			if json.Unmarshal(b, &s) != nil {
				continue
			}
			now := time.Now().UTC().Format(time.RFC3339)
			switch s.State {
			case "running":
				c.WriteJSON(CompileStarted{Type: "compileStarted", ProjectID: projectID, TS: now, JobID: jobID, Revision: rev, StartedAt: s.StartedAt})
			case "success":
				c.WriteJSON(CompileSucceeded{Type: "compileSucceeded", ProjectID: projectID, TS: now, JobID: jobID, Revision: rev, OutputPath: "/files/" + projectID + "/output.pdf", FinishedAt: s.FinishedAt})
				return
			case "failed":
				logTail, _ := os.ReadFile(logPath)
				c.WriteJSON(CompileFailed{Type: "compileFailed", ProjectID: projectID, TS: now, JobID: jobID, Revision: rev, Error: string(logTail), FinishedAt: s.FinishedAt})
				return
			}
		}
	}
}

func enqueueJob(projectID, entryFile, engine, revision, requestor string) (string, error) {
	jobID := uuid()
	root := projectDir(projectID)
	job := map[string]any{
		"jobId":     jobID,
		"projectId": projectID,
		"entryFile": entryFile,
		"engine":    engine,
		"revision":  revision,
	}
	b, _ := json.MarshalIndent(job, "", "  ")
	qpath := filepath.Join(root, "compile", "queue", jobID+".json")
	if err := os.WriteFile(qpath, b, 0o644); err != nil {
		return "", err
	}
	if isSimulationEnabled() {
		go simulateCompilation(projectID, jobID, entryFile, engine, revision)
	}
	return jobID, nil
}

func sendAck(c *websocket.Conn, projectID, op string, revision any, errObj map[string]any) {
	ack := map[string]any{
		"type":      "ack",
		"projectId": projectID,
		"ts":        time.Now().UTC().Format(time.RFC3339),
		"op":        op,
		"revision":  revision,
	}
	if errObj != nil {
		ack["error"] = errObj
	}
	c.WriteJSON(ack)
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
	cleanRel := path.Clean(filepath.ToSlash(requested))
	if strings.HasPrefix(cleanRel, "../") || strings.Contains(cleanRel, "/../") {
		return "", false
	}
	full := filepath.Join(root, cleanRel)
	if !strings.HasPrefix(full, filepath.Clean(root)) {
		return "", false
	}
	return full, true
}

func isSimulationEnabled() bool {
	v := os.Getenv("SIMULATE_COMPILER")
	return v == "1" || strings.ToLower(v) == "true"
}

func simulateCompilation(projectID, jobID, entryFile, engine, revision string) {
	time.Sleep(1 * time.Second)
	root := projectDir(projectID)
	statusPath := filepath.Join(root, "compile", "status", jobID+".json")
	logPath := filepath.Join(root, "compile", "logs", jobID+".txt")

	os.WriteFile(statusPath, []byte(`{"state":"running"}`), 0o644)
	os.WriteFile(logPath, []byte("Simulation running...\n"), 0o644)
	time.Sleep(2 * time.Second)

	currentContent := projectState.buffers[projectID][coalesce(entryFile, "main.tex")]
	writePlaceholderPDF(filepath.Join(root, "output.pdf"), currentContent)

	os.WriteFile(statusPath, []byte(`{"state":"success"}`), 0o644)
}

func writePlaceholderPDF(dst, content string) error {
	// A minimal PDF structure
	pdfContent := fmt.Sprintf(
		"%%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n"+
			"2 0 obj<</Type/Pages/Count 1/Kids[3 0 R]>>endobj\n"+
			"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>endobj\n"+
			"4 0 obj<</Length %d>>stream\nBT /F1 12 Tf 72 720 Td (%s) Tj ET\nendstream endobj\n"+
			"5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\n"+
			"xref\n0 6\n0000000000 65535 f \n"+
			"0000000010 00000 n \n0000000065 00000 n \n0000000122 00000 n \n"+
			"0000000280 00000 n \n0000000425 00000 n \n"+
			"trailer<</Size 6/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF",
		len(content)+25, content, 515+len(content)-len("Hello, LaTeX Preview!"),
	)
	return os.WriteFile(dst, []byte(pdfContent), 0o644)
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func coalesce[T comparable](v, def T) T {
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
