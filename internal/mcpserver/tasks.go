package mcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// TaskStatus defines the execution status of an asynchronous background task.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// AsyncTask holds metadata and state for an asynchronous operation.
type AsyncTask struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Target      string         `json:"target"`
	Status      TaskStatus     `json:"status"`
	Progress    string         `json:"progress,omitempty"`
	Error       string         `json:"error,omitempty"`
	Result      map[string]any `json:"result,omitempty"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

// TaskManager coordinates asynchronous background tasks with in-memory tracking.
type TaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*AsyncTask
}

// NewTaskManager constructs an in-memory TaskManager.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*AsyncTask),
	}
}

// CreateTask registers a new task in pending state and returns its assigned ID.
func (m *TaskManager) CreateTask(taskType, target string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := "task_" + hex.EncodeToString(b)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.tasks[id] = &AsyncTask{
		ID:        id,
		Type:      taskType,
		Target:    target,
		Status:    TaskStatusPending,
		StartedAt: time.Now().UTC(),
	}
	return id
}

// SetRunning marks a task as currently running.
func (m *TaskManager) SetRunning(id, progress string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		t.Status = TaskStatusRunning
		t.Progress = progress
	}
}

// SetCompleted marks a task as completed with its resulting data.
func (m *TaskManager) SetCompleted(id string, result map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		t.Status = TaskStatusCompleted
		t.Progress = "completed"
		t.Result = result
		now := time.Now().UTC()
		t.CompletedAt = &now
	}
}

// SetFailed marks a task as failed with an error message.
func (m *TaskManager) SetFailed(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		t.Status = TaskStatusFailed
		t.Progress = "failed"
		if err != nil {
			t.Error = err.Error()
		}
		now := time.Now().UTC()
		t.CompletedAt = &now
	}
}

// GetTask returns a snapshot copy of a task's state.
func (m *TaskManager) GetTask(id string) (*AsyncTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil, false
	}
	// Return shallow copy
	cp := *t
	return &cp, true
}
