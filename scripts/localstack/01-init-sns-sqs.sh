#!/bin/bash
set -e

echo "=================================================="
echo "Initializing SNS and SQS for Arbitrage Bot"
echo "=================================================="

# Create SNS Topic
echo "Creating SNS topic: arbitrage-opportunities..."
TOPIC_ARN=$(awslocal sns create-topic \
    --name arbitrage-opportunities \
    --output text \
    --query 'TopicArn' 2>/dev/null || echo "arn:aws:sns:us-east-1:000000000000:arbitrage-opportunities")
echo "✓ SNS Topic created: $TOPIC_ARN"

# Create SQS Dead Letter Queues
echo ""
echo "Creating Dead Letter Queues..."

awslocal sqs create-queue \
    --queue-name persistence-dlq \
    --attributes '{"MessageRetentionPeriod":"1209600"}' \
    > /dev/null 2>&1 || echo "  persistence-dlq already exists"

awslocal sqs create-queue \
    --queue-name webhooks-dlq \
    --attributes '{"MessageRetentionPeriod":"1209600"}' \
    > /dev/null 2>&1 || echo "  webhooks-dlq already exists"

echo "✓ DLQs created"

# Get DLQ ARNs
PERSISTENCE_DLQ_ARN=$(awslocal sqs get-queue-attributes \
    --queue-url http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/persistence-dlq \
    --attribute-names QueueArn \
    --output text \
    --query 'Attributes.QueueArn' 2>/dev/null || echo "")

WEBHOOKS_DLQ_ARN=$(awslocal sqs get-queue-attributes \
    --queue-url http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/webhooks-dlq \
    --attribute-names QueueArn \
    --output text \
    --query 'Attributes.QueueArn' 2>/dev/null || echo "")

# Create SQS Queues with DLQ configuration
echo ""
echo "Creating SQS queues..."

# Persistence queue
awslocal sqs create-queue \
    --queue-name persistence \
    --attributes "{
        \"VisibilityTimeout\":\"30\",
        \"MessageRetentionPeriod\":\"1209600\",
        \"RedrivePolicy\":\"{\\\"deadLetterTargetArn\\\":\\\"$PERSISTENCE_DLQ_ARN\\\",\\\"maxReceiveCount\\\":\\\"3\\\"}\"
    }" \
    > /dev/null 2>&1 || echo "  persistence queue already exists"

# Webhooks queue
awslocal sqs create-queue \
    --queue-name webhooks \
    --attributes "{
        \"VisibilityTimeout\":\"30\",
        \"MessageRetentionPeriod\":\"1209600\",
        \"RedrivePolicy\":\"{\\\"deadLetterTargetArn\\\":\\\"$WEBHOOKS_DLQ_ARN\\\",\\\"maxReceiveCount\\\":\\\"3\\\"}\"
    }" \
    > /dev/null 2>&1 || echo "  webhooks queue already exists"

echo "✓ SQS queues created"

# Get Queue ARNs
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

# Subscribe SQS queues to SNS topic
echo ""
echo "Subscribing SQS queues to SNS topic..."

# Persistence subscription (no filter - receives all messages)
awslocal sns subscribe \
    --topic-arn "$TOPIC_ARN" \
    --protocol sqs \
    --notification-endpoint "$PERSISTENCE_QUEUE_ARN" \
    --attributes '{"RawMessageDelivery":"true"}' \
    > /dev/null 2>&1 || echo "  persistence subscription already exists"

# Webhooks subscription (with filter for webhook channel)
awslocal sns subscribe \
    --topic-arn "$TOPIC_ARN" \
    --protocol sqs \
    --notification-endpoint "$WEBHOOKS_QUEUE_ARN" \
    --attributes '{
        "RawMessageDelivery":"true",
        "FilterPolicy":"{\"channel\":[\"webhook\"]}"
    }' \
    > /dev/null 2>&1 || echo "  webhooks subscription already exists"

echo "✓ Subscriptions created"

# Verification
echo ""
echo "=================================================="
echo "SNS/SQS Setup Summary"
echo "=================================================="

echo "SNS Topics:"
awslocal sns list-topics --output text --query 'Topics[*].TopicArn' | sed 's/^/  /'

echo ""
echo "SQS Queues:"
awslocal sqs list-queues --output text --query 'QueueUrls[]' | sed 's/^/  /'

echo ""
echo "✓ SNS/SQS initialization complete!"
