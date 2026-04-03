package jarvis

import (
	"time"

	"github.com/Gentleman-Programming/engram/internal/athena"
	"github.com/Gentleman-Programming/engram/internal/cloud/cloudstore"
)

// StoreAdapter wraps cloudstore.CloudStore to satisfy StoreInterface.
type StoreAdapter struct {
	cs *cloudstore.CloudStore
}

// NewStoreAdapter creates a new adapter.
func NewStoreAdapter(cs *cloudstore.CloudStore) *StoreAdapter {
	return &StoreAdapter{cs: cs}
}

func (a *StoreAdapter) AddMessage(conversationID int64, role, content, model string, tokensIn, tokensOut *int, costUSD *float64) (int64, error) {
	return a.cs.AddMessage(conversationID, role, content, model, tokensIn, tokensOut, costUSD)
}

func (a *StoreAdapter) GetMessages(conversationID int64, limit int) ([]StoreMessage, error) {
	msgs, err := a.cs.GetMessages(conversationID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]StoreMessage, len(msgs))
	for i, m := range msgs {
		result[i] = StoreMessage{Role: m.Role, Content: m.Content}
	}
	return result, nil
}

func (a *StoreAdapter) BudgetUsage(userID string, month time.Time, claudeBudget, openAIBudget float64) (*StoreBudgetReport, error) {
	r, err := a.cs.BudgetUsage(userID, month, claudeBudget, openAIBudget)
	if err != nil {
		return nil, err
	}
	return &StoreBudgetReport{
		ClaudeUsed:   r.ClaudeUsed,
		ClaudeBudget: r.ClaudeBudget,
		ClaudePct:    r.ClaudePct,
	}, nil
}

func (a *StoreAdapter) CreateTask(userID string, title, description, project, priority string) (int64, error) {
	return a.cs.CreateTask(userID, cloudstore.CreateTaskParams{
		Title:       title,
		Description: description,
		Project:     project,
		Priority:    priority,
		Source:      "jarvis",
	})
}

func (a *StoreAdapter) ListTasks(userID string, status string, limit int) ([]StoreTask, error) {
	tasks, _, err := a.cs.ListTasks(userID, cloudstore.ListTasksOpts{
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return nil, err
	}
	result := make([]StoreTask, len(tasks))
	for i, t := range tasks {
		project := ""
		if t.Project != nil {
			project = *t.Project
		}
		result[i] = StoreTask{
			ID:       t.ID,
			Title:    t.Title,
			Status:   t.Status,
			Priority: t.Priority,
			Project:  project,
		}
	}
	return result, nil
}

func (a *StoreAdapter) UpdateTaskStatus(userID string, taskID int64, status string) error {
	return a.cs.UpdateTask(userID, taskID, cloudstore.UpdateTaskParams{
		Status: &status,
	})
}

func (a *StoreAdapter) UpdateTask(userID string, taskID int64, fields athena.UpdateTaskFields) error {
	return a.cs.UpdateTask(userID, taskID, cloudstore.UpdateTaskParams{
		Title:       fields.Title,
		Description: fields.Description,
		Priority:    fields.Priority,
		Status:      fields.Status,
	})
}
