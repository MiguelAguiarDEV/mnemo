package cloudserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Gentleman-Programming/engram/internal/cloud/cloudstore"
)

// ─── Task Handlers ──────────────────────────────────────────────────────────

func (s *CloudServer) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	var body cloudstore.CreateTaskParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Title == "" {
		jsonError(w, http.StatusBadRequest, "title is required")
		return
	}

	id, err := s.store.CreateTask(userID, body)
	if err != nil {
		writeStoreError(w, err, "failed to create task")
		return
	}

	task, err := s.store.GetTask(userID, id)
	if err != nil {
		writeStoreError(w, err, "failed to fetch created task")
		return
	}

	jsonResponse(w, http.StatusCreated, task)
}

func (s *CloudServer) handleListTasks(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	opts := cloudstore.ListTasksOpts{
		Project:  r.URL.Query().Get("project"),
		Status:   r.URL.Query().Get("status"),
		Assignee: r.URL.Query().Get("assignee"),
		Limit:    queryInt(r, "limit", 50),
		Offset:   queryInt(r, "offset", 0),
	}

	tasks, total, err := s.store.ListTasks(userID, opts)
	if err != nil {
		writeStoreError(w, err, "failed to list tasks")
		return
	}

	if tasks == nil {
		tasks = []cloudstore.Task{}
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"tasks": tasks,
		"total": total,
	})
}

func (s *CloudServer) handleGetTask(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid task id")
		return
	}

	task, err := s.store.GetTask(userID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "not found") {
			jsonError(w, http.StatusNotFound, "task not found")
			return
		}
		writeStoreError(w, err, "failed to get task")
		return
	}

	subtasks, err := s.store.GetSubtasks(userID, id)
	if err != nil {
		writeStoreError(w, err, "failed to get subtasks")
		return
	}
	if subtasks == nil {
		subtasks = []cloudstore.Task{}
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"task":     task,
		"subtasks": subtasks,
	})
}

func (s *CloudServer) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid task id")
		return
	}

	var body cloudstore.UpdateTaskParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	err = s.store.UpdateTask(userID, id, body)
	if err != nil {
		if strings.Contains(err.Error(), "invalid transition") {
			jsonError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "not found") {
			jsonError(w, http.StatusNotFound, "task not found")
			return
		}
		writeStoreError(w, err, "failed to update task")
		return
	}

	task, err := s.store.GetTask(userID, id)
	if err != nil {
		writeStoreError(w, err, "failed to fetch updated task")
		return
	}

	jsonResponse(w, http.StatusOK, task)
}

func (s *CloudServer) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid task id")
		return
	}

	err = s.store.DeleteTask(userID, id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			jsonError(w, http.StatusNotFound, "task not found")
			return
		}
		writeStoreError(w, err, "failed to delete task")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *CloudServer) handleCreateSubtask(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	parentID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid task id")
		return
	}

	var body cloudstore.CreateTaskParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Title == "" {
		jsonError(w, http.StatusBadRequest, "title is required")
		return
	}

	id, err := s.store.CreateSubtask(userID, parentID, body)
	if err != nil {
		if strings.Contains(err.Error(), "parent") {
			jsonError(w, http.StatusNotFound, "parent task not found")
			return
		}
		writeStoreError(w, err, "failed to create subtask")
		return
	}

	task, err := s.store.GetTask(userID, id)
	if err != nil {
		writeStoreError(w, err, "failed to fetch created subtask")
		return
	}

	jsonResponse(w, http.StatusCreated, task)
}
