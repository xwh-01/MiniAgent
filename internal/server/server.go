package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/go-code-agent/internal/config"
	"github.com/example/go-code-agent/internal/runtime"
)

// Options configures the HTTP worker server.
type Options struct {
	BaseConfig config.Config
	OutputDir  string
	Workers    int
	QueueSize  int
}

type Server struct {
	baseConfig config.Config
	outputDir  string
	queue      chan *Job
	mux        *http.ServeMux

	mu   sync.RWMutex
	jobs map[string]*Job
}

type JobStatus string

const (
	StatusQueued    JobStatus = "queued"
	StatusRunning   JobStatus = "running"
	StatusSucceeded JobStatus = "succeeded"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
)

type Job struct {
	ID        string                   `json:"id"`
	Status    JobStatus                `json:"status"`
	Error     string                   `json:"error,omitempty"`
	CreatedAt time.Time                `json:"created_at"`
	StartedAt time.Time                `json:"started_at,omitempty"`
	EndedAt   time.Time                `json:"ended_at,omitempty"`
	OutputDir string                   `json:"output_dir"`
	Result    *runtime.SingleRunResult `json:"result,omitempty"`

	cfg    config.Config
	ctx    context.Context
	cancel context.CancelFunc
}

type RunRequest struct {
	Task               string `json:"task"`
	TaskFile           string `json:"task_file"`
	Workspace          string `json:"workspace"`
	OutputDir          string `json:"out"`
	ConfigFile         string `json:"config"`
	Env                string `json:"env"`
	Image              string `json:"image"`
	ContainerName      string `json:"container_name"`
	ContainerWorkspace string `json:"container_workspace"`
	KeepContainer      *bool  `json:"keep_container"`

	Provider    string   `json:"provider"`
	BaseURL     string   `json:"base_url"`
	Model       string   `json:"model"`
	APIKeyEnv   string   `json:"api_key_env"`
	ReplayFile  string   `json:"replay_file"`
	Temperature *float64 `json:"temperature"`
	MaxTokens   int      `json:"max_tokens"`

	MaxSteps            int    `json:"max_steps"`
	CommandTimeout      string `json:"command_timeout"`
	MaxObservationChars int    `json:"max_observation_chars"`
	SystemPrompt        string `json:"system_prompt"`
	SystemPromptFile    string `json:"system_prompt_file"`
}

type CreateRunResponse struct {
	ID        string    `json:"id"`
	Status    JobStatus `json:"status"`
	OutputDir string    `json:"output_dir"`
}

// New constructs a server and starts worker goroutines.
func New(opts Options) (*Server, error) {
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = opts.Workers * 8
		if opts.QueueSize < 8 {
			opts.QueueSize = 8
		}
	}
	if opts.OutputDir == "" {
		opts.OutputDir = "runs/server"
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return nil, err
	}
	s := &Server{
		baseConfig: opts.BaseConfig,
		outputDir:  opts.OutputDir,
		queue:      make(chan *Job, opts.QueueSize),
		mux:        http.NewServeMux(),
		jobs:       map[string]*Job{},
	}
	s.routes()
	for i := 0; i < opts.Workers; i++ {
		go s.worker()
	}
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/v1/runs", s.handleRuns)
	s.mux.HandleFunc("/v1/runs/", s.handleRunByID)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createRun(w, r)
	case http.MethodGet:
		s.listRuns(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req RunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}
	cfg, err := s.configFromRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := newID()
	if strings.TrimSpace(req.OutputDir) == "" {
		cfg.OutputDir = filepath.Join(s.outputDir, id)
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{
		ID:        id,
		Status:    StatusQueued,
		CreatedAt: time.Now(),
		OutputDir: cfg.OutputDir,
		cfg:       cfg,
		ctx:       ctx,
		cancel:    cancel,
	}
	s.mu.Lock()
	s.jobs[id] = job
	s.mu.Unlock()

	select {
	case s.queue <- job:
		writeJSON(w, http.StatusAccepted, CreateRunResponse{ID: id, Status: StatusQueued, OutputDir: job.OutputDir})
	default:
		s.mu.Lock()
		delete(s.jobs, id)
		s.mu.Unlock()
		cancel()
		writeError(w, http.StatusServiceUnavailable, errors.New("server queue is full"))
	}
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid limit %q", raw))
			return
		}
		limit = n
	}
	s.mu.RLock()
	jobs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, snapshotJob(j))
	}
	s.mu.RUnlock()
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	if limit < len(jobs) {
		jobs = jobs[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": jobs})
}

func (s *Server) handleRunByID(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, errors.New("missing run id"))
		return
	}
	id := parts[0]
	s.mu.RLock()
	job := s.jobs[id]
	s.mu.RUnlock()
	if job == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("run %s not found", id))
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, snapshotJob(job))
		case http.MethodDelete:
			s.cancelJob(w, job)
		default:
			methodNotAllowed(w)
		}
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	switch parts[1] {
	case "trajectory":
		serveArtifact(w, r, job, "trajectory.json")
	case "patch":
		serveArtifact(w, r, job, "patch.diff")
	case "result":
		serveArtifact(w, r, job, "run.json")
	default:
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown artifact %q", parts[1]))
	}
}

func (s *Server) cancelJob(w http.ResponseWriter, job *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.Status == StatusSucceeded || job.Status == StatusFailed || job.Status == StatusCancelled {
		writeJSON(w, http.StatusOK, snapshotJob(job))
		return
	}
	job.cancel()
	job.Status = StatusCancelled
	job.EndedAt = time.Now()
	writeJSON(w, http.StatusOK, snapshotJob(job))
}

func serveArtifact(w http.ResponseWriter, r *http.Request, job *Job, name string) {
	path := filepath.Join(job.OutputDir, name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Errorf("artifact %s not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.ServeFile(w, r, path)
}

func (s *Server) worker() {
	for job := range s.queue {
		s.runJob(job)
	}
}

func (s *Server) runJob(job *Job) {
	s.mu.Lock()
	if job.Status == StatusCancelled {
		s.mu.Unlock()
		return
	}
	job.Status = StatusRunning
	job.StartedAt = time.Now()
	s.mu.Unlock()

	res, err := runtime.RunSingle(job.ctx, job.cfg)

	s.mu.Lock()
	defer s.mu.Unlock()
	job.Result = res
	job.EndedAt = time.Now()
	if job.Status == StatusCancelled || errors.Is(job.ctx.Err(), context.Canceled) {
		job.Status = StatusCancelled
		if err != nil {
			job.Error = err.Error()
		}
		return
	}
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
		return
	}
	job.Status = StatusSucceeded
}

func (s *Server) configFromRequest(req RunRequest) (config.Config, error) {
	cfg := s.baseConfig
	if strings.TrimSpace(req.ConfigFile) != "" {
		loaded, err := config.LoadFile(req.ConfigFile, cfg)
		if err != nil {
			return cfg, fmt.Errorf("load request config: %w", err)
		}
		cfg = loaded
	}
	if req.Task != "" {
		cfg.Task = req.Task
		cfg.TaskFile = ""
	}
	if req.TaskFile != "" {
		cfg.TaskFile = req.TaskFile
	}
	if req.Workspace != "" {
		cfg.Workspace = req.Workspace
	}
	if req.OutputDir != "" {
		cfg.OutputDir = req.OutputDir
	}
	if req.Env != "" {
		cfg.Environment.Type = req.Env
	}
	if req.Image != "" {
		cfg.Environment.DockerImage = req.Image
	}
	if req.ContainerName != "" {
		cfg.Environment.ContainerName = req.ContainerName
	}
	if req.ContainerWorkspace != "" {
		cfg.Environment.ContainerWorkspace = req.ContainerWorkspace
	}
	if req.KeepContainer != nil {
		cfg.Environment.KeepContainer = *req.KeepContainer
	}
	if req.Provider != "" {
		cfg.Model.Provider = req.Provider
	}
	if req.BaseURL != "" {
		cfg.Model.BaseURL = req.BaseURL
	}
	if req.Model != "" {
		cfg.Model.Model = req.Model
	}
	if req.APIKeyEnv != "" {
		cfg.Model.APIKeyEnv = req.APIKeyEnv
	}
	if req.ReplayFile != "" {
		cfg.Model.ReplayFile = req.ReplayFile
	}
	if req.Temperature != nil {
		cfg.Model.Temperature = *req.Temperature
	}
	if req.MaxTokens != 0 {
		cfg.Model.MaxTokens = req.MaxTokens
	}
	if req.MaxSteps != 0 {
		cfg.Agent.MaxSteps = req.MaxSteps
	}
	if req.CommandTimeout != "" {
		d, err := time.ParseDuration(req.CommandTimeout)
		if err != nil {
			seconds, secErr := strconv.Atoi(req.CommandTimeout)
			if secErr != nil {
				return cfg, fmt.Errorf("invalid command_timeout %q", req.CommandTimeout)
			}
			d = time.Duration(seconds) * time.Second
		}
		cfg.Agent.CommandTimeout = d
	}
	if req.MaxObservationChars != 0 {
		cfg.Agent.MaxObservationChars = req.MaxObservationChars
	}
	if req.SystemPrompt != "" {
		cfg.Agent.SystemPrompt = req.SystemPrompt
	}
	if req.SystemPromptFile != "" {
		cfg.Agent.SystemPromptFile = req.SystemPromptFile
	}
	if strings.TrimSpace(cfg.Task) == "" && strings.TrimSpace(cfg.TaskFile) == "" {
		return cfg, errors.New("missing task or task_file")
	}
	return cfg, nil
}

func snapshotJob(j *Job) *Job {
	cp := *j
	cp.cfg = config.Config{}
	cp.ctx = nil
	cp.cancel = nil
	return &cp
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
