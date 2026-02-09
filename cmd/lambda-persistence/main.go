package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/arbitrage"
)

var (
	dynamoClient *dynamodb.Client
	tableName    string
)

func init() {
	// Load AWS SDK config
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		panic(fmt.Sprintf("failed to load AWS config: %v", err))
	}

	// Initialize DynamoDB client
	dynamoClient = dynamodb.NewFromConfig(cfg)

	// Get table name from environment
	tableName = "arbitrage-opportunities" // Default for LocalStack

	fmt.Printf("[INIT] Persistence Lambda initialized - Table: %s\n", tableName)
}

// DynamoDBRecord represents a DynamoDB record with TTL
type DynamoDBRecord struct {
	arbitrage.SerializableOpportunity
	TTL int64 `dynamodbav:"ttl" json:"ttl"` // Auto-expire after 7 days
}

// Handler processes SQS events and writes to DynamoDB
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

		// Create DynamoDB record with TTL
		oppRecord := DynamoDBRecord{
			SerializableOpportunity: opportunity,
			TTL:                     time.Now().Unix() + (7 * 24 * 60 * 60), // 7 days TTL
		}

		// Write to DynamoDB
		if err := writeToDynamoDB(ctx, oppRecord); err != nil {
			fmt.Printf("[ERROR] Failed to write to DynamoDB: %v - OpportunityID: %s\n",
				err, oppRecord.OpportunityID)
			batchItemFailures = append(batchItemFailures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
			continue
		}

		successCount++
		fmt.Printf("[SUCCESS] Persisted opportunity: %s (Block: %d, Profit: $%s)\n",
			oppRecord.OpportunityID,
			oppRecord.BlockNumber,
			oppRecord.NetProfitUSD,
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

// writeToDynamoDB writes an opportunity record to DynamoDB
func writeToDynamoDB(ctx context.Context, record DynamoDBRecord) error {
	// Marshal record to DynamoDB attribute values
	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return fmt.Errorf("failed to marshal record: %w", err)
	}

	// Put item
	_, err = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("failed to put item: %w", err)
	}

	return nil
}

func main() {
	lambda.Start(Handler)
}
