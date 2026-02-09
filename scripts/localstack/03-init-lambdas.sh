#!/bin/bash
set -e

echo "=================================================="
echo "Initializing Lambda Functions"
echo "=================================================="

# Check if Lambda zips exist
if [ ! -f "/tmp/lambdas/persistence.zip" ] || [ ! -f "/tmp/lambdas/webhook.zip" ]; then
    echo "⚠ Lambda zips not found in /tmp/lambdas/"
    echo "  Run 'make build-lambdas' first"
    echo "  Skipping Lambda creation..."
    exit 0
fi

echo "✓ Lambda zips found"

# Create persistence Lambda function
echo ""
echo "Creating Lambda function: persistence..."
awslocal lambda create-function \
    --function-name persistence \
    --runtime provided.al2 \
    --handler bootstrap \
    --role arn:aws:iam::000000000000:role/lambda-role \
    --zip-file fileb:///tmp/lambdas/persistence.zip \
    --timeout 30 \
    --memory-size 256 \
    --environment "Variables={
        DYNAMODB_TABLE_NAME=arbitrage-opportunities,
        AWS_REGION=us-east-1
    }" \
    > /dev/null 2>&1 || echo "  Function already exists"

echo "✓ persistence Lambda created"

# Create webhook Lambda function
echo ""
echo "Creating Lambda function: webhook..."
awslocal lambda create-function \
    --function-name webhook \
    --runtime provided.al2 \
    --handler bootstrap \
    --role arn:aws:iam::000000000000:role/lambda-role \
    --zip-file fileb:///tmp/lambdas/webhook.zip \
    --timeout 30 \
    --memory-size 256 \
    --environment "Variables={
        AWS_REGION=us-east-1
    }" \
    > /dev/null 2>&1 || echo "  Function already exists"

echo "✓ webhook Lambda created"

# Get SQS Queue ARNs
PERSISTENCE_QUEUE_ARN=$(awslocal sqs get-queue-attributes \
    --queue-url http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/persistence \
    --attribute-names QueueArn \
    --output text \
    --query 'Attributes.QueueArn' 2>/dev/null || echo "")

WEBHOOKS_QUEUE_ARN=$(awslocal sqs get-queue-attributes \
    --queue-url http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/webhooks \
    --attribute-names QueueArn \
    --output text \
    --query 'Attributes.QueueArn' 2>/dev/null || echo "")

# Create event source mappings
echo ""
echo "Creating event source mappings..."

# Persistence Lambda mapping
if [ -n "$PERSISTENCE_QUEUE_ARN" ]; then
    awslocal lambda create-event-source-mapping \
        --function-name persistence \
        --event-source-arn "$PERSISTENCE_QUEUE_ARN" \
        --batch-size 10 \
        --enabled \
        > /dev/null 2>&1 || echo "  persistence mapping already exists"
    echo "✓ persistence event source mapping created"
else
    echo "  ⚠ persistence queue not found, skipping mapping"
fi

# Webhook Lambda mapping
if [ -n "$WEBHOOKS_QUEUE_ARN" ]; then
    awslocal lambda create-event-source-mapping \
        --function-name webhook \
        --event-source-arn "$WEBHOOKS_QUEUE_ARN" \
        --batch-size 10 \
        --enabled \
        > /dev/null 2>&1 || echo "  webhook mapping already exists"
    echo "✓ webhook event source mapping created"
else
    echo "  ⚠ webhooks queue not found, skipping mapping"
fi

# Verification
echo ""
echo "=================================================="
echo "Lambda Functions Summary"
echo "=================================================="

echo "Functions:"
awslocal lambda list-functions --output text --query 'Functions[*].FunctionName' | sed 's/^/  /'

echo ""
echo "Event Source Mappings:"
awslocal lambda list-event-source-mappings --output json 2>/dev/null | \
    jq -r '.EventSourceMappings[] | "  \(.FunctionArn) <- \(.EventSourceArn)"' 2>/dev/null || echo "  None"

echo ""
echo "✓ Lambda initialization complete!"
