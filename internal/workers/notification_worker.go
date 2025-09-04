package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"brokle/internal/config"
)

// NotificationWorker handles async notification processing
type NotificationWorker struct {
	config *config.Config
	logger *logrus.Logger
	queue  chan NotificationJob
	quit   chan bool
}

// NotificationJob represents a notification processing job
type NotificationJob struct {
	Type      string      `json:"type"`
	Data      interface{} `json:"data"`
	Timestamp time.Time   `json:"timestamp"`
	Retry     int         `json:"retry"`
}

// EmailJob represents an email notification job
type EmailJob struct {
	To          []string          `json:"to"`
	CC          []string          `json:"cc,omitempty"`
	BCC         []string          `json:"bcc,omitempty"`
	Subject     string            `json:"subject"`
	Body        string            `json:"body"`
	BodyHTML    string            `json:"body_html,omitempty"`
	Template    string            `json:"template,omitempty"`
	TemplateData map[string]interface{} `json:"template_data,omitempty"`
	Priority    string            `json:"priority,omitempty"`
	UserID      string            `json:"user_id,omitempty"`
}

// WebhookJob represents a webhook notification job
type WebhookJob struct {
	URL         string                 `json:"url"`
	Method      string                 `json:"method"`
	Headers     map[string]string      `json:"headers,omitempty"`
	Payload     map[string]interface{} `json:"payload"`
	Timeout     int                    `json:"timeout,omitempty"`
	RetryCount  int                    `json:"retry_count,omitempty"`
	UserID      string                 `json:"user_id,omitempty"`
	EventType   string                 `json:"event_type,omitempty"`
}

// SlackJob represents a Slack notification job
type SlackJob struct {
	Channel   string                 `json:"channel"`
	Message   string                 `json:"message"`
	Username  string                 `json:"username,omitempty"`
	IconEmoji string                 `json:"icon_emoji,omitempty"`
	Blocks    []map[string]interface{} `json:"blocks,omitempty"`
	UserID    string                 `json:"user_id,omitempty"`
	EventType string                 `json:"event_type,omitempty"`
}

// SMSJob represents an SMS notification job
type SMSJob struct {
	To      string `json:"to"`
	Message string `json:"message"`
	UserID  string `json:"user_id,omitempty"`
}

// PushJob represents a push notification job
type PushJob struct {
	DeviceTokens []string               `json:"device_tokens"`
	Title        string                 `json:"title"`
	Body         string                 `json:"body"`
	Data         map[string]interface{} `json:"data,omitempty"`
	Badge        int                    `json:"badge,omitempty"`
	Sound        string                 `json:"sound,omitempty"`
	UserID       string                 `json:"user_id,omitempty"`
}

// NewNotificationWorker creates a new notification worker
func NewNotificationWorker(
	config *config.Config,
	logger *logrus.Logger,
) *NotificationWorker {
	return &NotificationWorker{
		config: config,
		logger: logger,
		queue:  make(chan NotificationJob, 500), // Buffer for 500 notifications
		quit:   make(chan bool),
	}
}

// Start starts the notification worker
func (w *NotificationWorker) Start() {
	w.logger.Info("Starting notification worker")

	// Start multiple worker goroutines
	numWorkers := w.config.Workers.NotificationWorkers
	if numWorkers == 0 {
		numWorkers = 2 // Default
	}

	for i := 0; i < numWorkers; i++ {
		go w.worker(i)
	}
}

// Stop stops the notification worker
func (w *NotificationWorker) Stop() {
	w.logger.Info("Stopping notification worker")
	close(w.quit)
}

// QueueJob queues a notification job for processing
func (w *NotificationWorker) QueueJob(jobType string, data interface{}) {
	job := NotificationJob{
		Type:      jobType,
		Data:      data,
		Timestamp: time.Now(),
		Retry:     0,
	}

	select {
	case w.queue <- job:
		w.logger.WithField("type", jobType).Debug("Notification job queued")
	default:
		w.logger.WithField("type", jobType).Warn("Notification queue full, dropping job")
	}
}

// QueueEmail queues an email notification
func (w *NotificationWorker) QueueEmail(email EmailJob) {
	w.QueueJob("email", email)
}

// QueueWebhook queues a webhook notification
func (w *NotificationWorker) QueueWebhook(webhook WebhookJob) {
	w.QueueJob("webhook", webhook)
}

// QueueSlack queues a Slack notification
func (w *NotificationWorker) QueueSlack(slack SlackJob) {
	w.QueueJob("slack", slack)
}

// QueueSMS queues an SMS notification
func (w *NotificationWorker) QueueSMS(sms SMSJob) {
	w.QueueJob("sms", sms)
}

// QueuePush queues a push notification
func (w *NotificationWorker) QueuePush(push PushJob) {
	w.QueueJob("push", push)
}

// worker processes jobs from the queue
func (w *NotificationWorker) worker(id int) {
	w.logger.WithField("worker_id", id).Info("Notification worker started")

	for {
		select {
		case job := <-w.queue:
			w.processJob(id, job)

		case <-w.quit:
			w.logger.WithField("worker_id", id).Info("Notification worker stopping")
			return
		}
	}
}

// processJob processes a single notification job
func (w *NotificationWorker) processJob(workerID int, job NotificationJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := w.logger.WithFields(logrus.Fields{
		"worker_id": workerID,
		"job_type":  job.Type,
		"retry":     job.Retry,
	})

	logger.Debug("Processing notification job")

	var err error
	switch job.Type {
	case "email":
		err = w.processEmail(ctx, job.Data)
	case "webhook":
		err = w.processWebhook(ctx, job.Data)
	case "slack":
		err = w.processSlack(ctx, job.Data)
	case "sms":
		err = w.processSMS(ctx, job.Data)
	case "push":
		err = w.processPush(ctx, job.Data)
	default:
		logger.Warn("Unknown notification job type")
		return
	}

	if err != nil {
		logger.WithError(err).Error("Failed to process notification job")

		// Retry logic with exponential backoff
		if job.Retry < 3 {
			job.Retry++
			delay := time.Duration(job.Retry*job.Retry) * time.Minute
			time.Sleep(delay)
			w.queue <- job
			logger.WithField("retry", job.Retry).Info("Retrying notification job")
		} else {
			logger.Error("Max retries exceeded, dropping notification job")
		}
	} else {
		logger.Debug("Notification job processed successfully")
	}
}

// processEmail processes an email notification
func (w *NotificationWorker) processEmail(ctx context.Context, data interface{}) error {
	jobData, ok := data.(EmailJob)
	if !ok {
		// Try to unmarshal if it's a map
		if mapData, ok := data.(map[string]interface{}); ok {
			jsonData, err := json.Marshal(mapData)
			if err != nil {
				return fmt.Errorf("failed to marshal email data: %w", err)
			}
			if err := json.Unmarshal(jsonData, &jobData); err != nil {
				return fmt.Errorf("failed to unmarshal email data: %w", err)
			}
		} else {
			return fmt.Errorf("invalid email data type")
		}
	}

	w.logger.WithFields(logrus.Fields{
		"to":       jobData.To,
		"subject":  jobData.Subject,
		"template": jobData.Template,
	}).Info("Sending email")

	// TODO: Implement actual email sending logic
	// This would integrate with services like SendGrid, AWS SES, etc.
	
	// Simulate email sending
	time.Sleep(100 * time.Millisecond)
	
	w.logger.WithField("to", jobData.To).Info("Email sent successfully")
	return nil
}

// processWebhook processes a webhook notification
func (w *NotificationWorker) processWebhook(ctx context.Context, data interface{}) error {
	jobData, ok := data.(WebhookJob)
	if !ok {
		// Try to unmarshal if it's a map
		if mapData, ok := data.(map[string]interface{}); ok {
			jsonData, err := json.Marshal(mapData)
			if err != nil {
				return fmt.Errorf("failed to marshal webhook data: %w", err)
			}
			if err := json.Unmarshal(jsonData, &jobData); err != nil {
				return fmt.Errorf("failed to unmarshal webhook data: %w", err)
			}
		} else {
			return fmt.Errorf("invalid webhook data type")
		}
	}

	w.logger.WithFields(logrus.Fields{
		"url":        jobData.URL,
		"method":     jobData.Method,
		"event_type": jobData.EventType,
	}).Info("Sending webhook")

	// TODO: Implement actual webhook sending logic
	// This would make HTTP requests to the specified URLs
	
	// Simulate webhook sending
	time.Sleep(200 * time.Millisecond)
	
	w.logger.WithField("url", jobData.URL).Info("Webhook sent successfully")
	return nil
}

// processSlack processes a Slack notification
func (w *NotificationWorker) processSlack(ctx context.Context, data interface{}) error {
	jobData, ok := data.(SlackJob)
	if !ok {
		// Try to unmarshal if it's a map
		if mapData, ok := data.(map[string]interface{}); ok {
			jsonData, err := json.Marshal(mapData)
			if err != nil {
				return fmt.Errorf("failed to marshal slack data: %w", err)
			}
			if err := json.Unmarshal(jsonData, &jobData); err != nil {
				return fmt.Errorf("failed to unmarshal slack data: %w", err)
			}
		} else {
			return fmt.Errorf("invalid slack data type")
		}
	}

	w.logger.WithFields(logrus.Fields{
		"channel":    jobData.Channel,
		"event_type": jobData.EventType,
	}).Info("Sending Slack message")

	// TODO: Implement actual Slack API integration
	// This would use Slack's Web API to send messages
	
	// Simulate Slack sending
	time.Sleep(150 * time.Millisecond)
	
	w.logger.WithField("channel", jobData.Channel).Info("Slack message sent successfully")
	return nil
}

// processSMS processes an SMS notification
func (w *NotificationWorker) processSMS(ctx context.Context, data interface{}) error {
	jobData, ok := data.(SMSJob)
	if !ok {
		// Try to unmarshal if it's a map
		if mapData, ok := data.(map[string]interface{}); ok {
			jsonData, err := json.Marshal(mapData)
			if err != nil {
				return fmt.Errorf("failed to marshal sms data: %w", err)
			}
			if err := json.Unmarshal(jsonData, &jobData); err != nil {
				return fmt.Errorf("failed to unmarshal sms data: %w", err)
			}
		} else {
			return fmt.Errorf("invalid sms data type")
		}
	}

	w.logger.WithField("to", jobData.To).Info("Sending SMS")

	// TODO: Implement actual SMS sending logic
	// This would integrate with services like Twilio, AWS SNS, etc.
	
	// Simulate SMS sending
	time.Sleep(100 * time.Millisecond)
	
	w.logger.WithField("to", jobData.To).Info("SMS sent successfully")
	return nil
}

// processPush processes a push notification
func (w *NotificationWorker) processPush(ctx context.Context, data interface{}) error {
	jobData, ok := data.(PushJob)
	if !ok {
		// Try to unmarshal if it's a map
		if mapData, ok := data.(map[string]interface{}); ok {
			jsonData, err := json.Marshal(mapData)
			if err != nil {
				return fmt.Errorf("failed to marshal push data: %w", err)
			}
			if err := json.Unmarshal(jsonData, &jobData); err != nil {
				return fmt.Errorf("failed to unmarshal push data: %w", err)
			}
		} else {
			return fmt.Errorf("invalid push data type")
		}
	}

	w.logger.WithFields(logrus.Fields{
		"devices": len(jobData.DeviceTokens),
		"title":   jobData.Title,
	}).Info("Sending push notifications")

	// TODO: Implement actual push notification logic
	// This would integrate with Firebase Cloud Messaging, Apple Push Notifications, etc.
	
	// Simulate push sending
	time.Sleep(300 * time.Millisecond)
	
	w.logger.WithField("devices", len(jobData.DeviceTokens)).Info("Push notifications sent successfully")
	return nil
}

// GetQueueLength returns the current queue length
func (w *NotificationWorker) GetQueueLength() int {
	return len(w.queue)
}

// GetStats returns worker statistics
func (w *NotificationWorker) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"queue_length":   w.GetQueueLength(),
		"queue_capacity": cap(w.queue),
		"workers":        w.config.Workers.NotificationWorkers,
	}
}

// Helper methods for common notifications

// SendWelcomeEmail sends a welcome email to new users
func (w *NotificationWorker) SendWelcomeEmail(userEmail, userName string) {
	w.QueueEmail(EmailJob{
		To:       []string{userEmail},
		Subject:  "Welcome to Brokle!",
		Template: "welcome",
		TemplateData: map[string]interface{}{
			"name": userName,
		},
		Priority: "normal",
	})
}

// SendPasswordResetEmail sends a password reset email
func (w *NotificationWorker) SendPasswordResetEmail(userEmail, resetToken string) {
	w.QueueEmail(EmailJob{
		To:       []string{userEmail},
		Subject:  "Reset your Brokle password",
		Template: "password_reset",
		TemplateData: map[string]interface{}{
			"reset_token": resetToken,
		},
		Priority: "high",
	})
}

// SendBillingAlert sends a billing alert notification
func (w *NotificationWorker) SendBillingAlert(userEmail string, amount float64, threshold float64) {
	w.QueueEmail(EmailJob{
		To:       []string{userEmail},
		Subject:  "Billing Alert - Usage Threshold Exceeded",
		Template: "billing_alert",
		TemplateData: map[string]interface{}{
			"amount":    amount,
			"threshold": threshold,
		},
		Priority: "high",
	})
}

// SendSystemAlert sends a system alert to administrators
func (w *NotificationWorker) SendSystemAlert(message, severity string) {
	// Send Slack notification
	w.QueueSlack(SlackJob{
		Channel:   "#alerts",
		Message:   fmt.Sprintf(":warning: *System Alert*\n*Severity:* %s\n*Message:* %s", severity, message),
		Username:  "Brokle Monitor",
		IconEmoji: ":warning:",
		EventType: "system_alert",
	})
	
	// Also send webhook if configured
	if w.config.Notifications.AlertWebhookURL != "" {
		w.QueueWebhook(WebhookJob{
			URL:    w.config.Notifications.AlertWebhookURL,
			Method: "POST",
			Payload: map[string]interface{}{
				"type":     "system_alert",
				"severity": severity,
				"message":  message,
				"timestamp": time.Now().Unix(),
			},
			EventType: "system_alert",
		})
	}
}