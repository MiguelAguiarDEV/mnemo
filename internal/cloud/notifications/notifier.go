// Package notifications provides a notification abstraction and concrete
// senders (Discord DM, etc.) for JARVIS alerts.
package notifications

// NotificationType classifies the urgency / intent of a notification.
type NotificationType string

const (
	TaskComplete NotificationType = "task_complete"
	InputNeeded  NotificationType = "input_needed"
	Alert        NotificationType = "alert"
	Info         NotificationType = "info"
)

// Notification is the payload sent through any Notifier implementation.
type Notification struct {
	Type    NotificationType `json:"type"`
	Title   string           `json:"title"`
	Message string           `json:"message"`
}

// Notifier sends a notification through a specific channel.
type Notifier interface {
	Send(n Notification) error
}
