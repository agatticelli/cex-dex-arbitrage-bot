package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/arbitrage"
)

var (
	httpClient *http.Client
	maxRetries = 3
	baseDelay  = 1 * time.Second
	timeout    = 5 * time.Second
)

func init() {
	// Initialize HTTP client with timeout
	httpClient = &http.Client{
		Timeout: timeout,
	}

	fmt.Println("[INIT] Webhook Lambda initialized")
}

// Handler processes SQS events and sends webhooks
func Handler(ctx context.Context, sqsEvent events.SQSEvent) (events.SQSEventResponse, error) {
	recordCount := len(sqsEvent.Records)
	fmt.Printf("[HANDLER] Processing %d SQS records\n", recordCount)

	var batchItemFailures []events.SQSBatchItemFailure
	successCount := 0

	for _, record := range sqsEvent.Records {
		// Parse SNS message from SQS record body
		var snsMessage struct {
			Message string `json:"Message"`
		}

		if err := json.Unmarshal([]byte(record.Body), &snsMessage); err != nil {
			fmt.Printf("[ERROR] Failed to parse SQS body: %v\n", err)
			batchItemFailures = append(batchItemFailures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
			continue
		}

		// Parse opportunity from SNS message using serializable type
		var opportunity arbitrage.SerializableOpportunity
		if err := json.Unmarshal([]byte(snsMessage.Message), &opportunity); err != nil {
			fmt.Printf("[ERROR] Failed to parse opportunity: %v\n", err)
			batchItemFailures = append(batchItemFailures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
			continue
		}

		// Get webhook URL from environment or message attributes
		webhookURL := os.Getenv("WEBHOOK_URL")
		if webhookURL == "" {
			// Try to get from message attributes if available
			if record.MessageAttributes != nil {
				if urlAttr, ok := record.MessageAttributes["webhookURL"]; ok {
					if urlAttr.StringValue != nil {
						webhookURL = *urlAttr.StringValue
					}
				}
			}
		}

		if webhookURL == "" {
			fmt.Printf("[WARN] No webhook URL configured, skipping record: %s\n", record.MessageId)
			// Don't fail - just skip if no URL is configured
			successCount++
			continue
		}

		// Send webhook with retry
		if err := sendWebhookWithRetry(ctx, webhookURL, opportunity); err != nil {
			fmt.Printf("[ERROR] Failed to send webhook after retries: %v - OpportunityID: %s\n",
				err, opportunity.OpportunityID)
			batchItemFailures = append(batchItemFailures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
			continue
		}

		successCount++
		fmt.Printf("[SUCCESS] Webhook sent: OpportunityID: %s, URL: %s\n",
			opportunity.OpportunityID,
			maskURL(webhookURL),
		)
	}

	fmt.Printf("[SUMMARY] Processed %d records - Success: %d, Failed: %d\n",
		recordCount, successCount, len(batchItemFailures))

	// Return partial batch failure response
	// SQS will retry only the failed messages
	return events.SQSEventResponse{
		BatchItemFailures: batchItemFailures,
	}, nil
}

// sendWebhookWithRetry sends a webhook with exponential backoff retry
func sendWebhookWithRetry(ctx context.Context, url string, payload arbitrage.SerializableOpportunity) error {
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Calculate exponential backoff with jitter
			delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
			jitter := time.Duration(float64(delay) * 0.1)
			sleepTime := delay + jitter

			fmt.Printf("[RETRY] Attempt %d/%d - waiting %v before retry\n", attempt+1, maxRetries, sleepTime)
			time.Sleep(sleepTime)
		}

		err := sendWebhook(ctx, url, payload)
		if err == nil {
			return nil // Success
		}

		lastErr = err

		// Check if error is retryable (5xx errors)
		if !isRetryableError(err) {
			fmt.Printf("[ERROR] Non-retryable error: %v\n", err)
			return err
		}
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// sendWebhook makes a single HTTP POST request
func sendWebhook(ctx context.Context, url string, payload arbitrage.SerializableOpportunity) error {
	// Marshal payload to JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Arbitrage-Bot-Webhook/1.0")

	// Send request
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil // Success
	}

	// Read response body for error details
	var respBody bytes.Buffer
	if _, err := respBody.ReadFrom(resp.Body); err == nil {
		fmt.Printf("[DEBUG] Response body: %s\n", respBody.String())
	}

	return &HTTPError{
		StatusCode: resp.StatusCode,
		Message:    fmt.Sprintf("webhook failed with status %d", resp.StatusCode),
	}
}

// HTTPError represents an HTTP error response
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return e.Message
}

// isRetryableError checks if an error should be retried
func isRetryableError(err error) bool {
	if httpErr, ok := err.(*HTTPError); ok {
		// Retry on 5xx server errors
		return httpErr.StatusCode >= 500 && httpErr.StatusCode < 600
	}

	// Retry on network errors
	return true
}

// maskURL masks sensitive parts of URL for logging
func maskURL(url string) string {
	if len(url) > 30 {
		return url[:15] + "..." + url[len(url)-10:]
	}
	return url
}

func main() {
	lambda.Start(Handler)
}
