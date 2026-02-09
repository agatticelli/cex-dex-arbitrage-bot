#!/bin/bash
set -e

echo "=================================================="
echo "Initializing DynamoDB Tables"
echo "=================================================="

# Create arbitrage-opportunities table
echo "Creating table: arbitrage-opportunities..."
awslocal dynamodb create-table \
    --table-name arbitrage-opportunities \
    --attribute-definitions \
        AttributeName=opportunityId,AttributeType=S \
        AttributeName=timestamp,AttributeType=N \
    --key-schema \
        AttributeName=opportunityId,KeyType=HASH \
        AttributeName=timestamp,KeyType=RANGE \
    --billing-mode PAY_PER_REQUEST \
    --global-secondary-indexes \
        "IndexName=TimestampIndex,KeySchema=[{AttributeName=timestamp,KeyType=HASH}],Projection={ProjectionType=ALL}" \
    > /dev/null 2>&1 || echo "  Table already exists"

echo "✓ arbitrage-opportunities table created"

# Create execution-history table
echo ""
echo "Creating table: execution-history..."
awslocal dynamodb create-table \
    --table-name execution-history \
    --attribute-definitions \
        AttributeName=executionId,AttributeType=S \
        AttributeName=blockNumber,AttributeType=N \
    --key-schema \
        AttributeName=executionId,KeyType=HASH \
        AttributeName=blockNumber,KeyType=RANGE \
    --billing-mode PAY_PER_REQUEST \
    > /dev/null 2>&1 || echo "  Table already exists"

echo "✓ execution-history table created"

# Enable TTL on arbitrage-opportunities table
echo ""
echo "Enabling TTL on arbitrage-opportunities..."
awslocal dynamodb update-time-to-live \
    --table-name arbitrage-opportunities \
    --time-to-live-specification \
        "Enabled=true,AttributeName=ttl" \
    > /dev/null 2>&1 || echo "  TTL already enabled"

echo "✓ TTL enabled"

# Verification
echo ""
echo "=================================================="
echo "DynamoDB Tables Summary"
echo "=================================================="

echo "Tables:"
awslocal dynamodb list-tables --output text --query 'TableNames[]' | sed 's/^/  /'

echo ""
echo "Table Details:"
for table in arbitrage-opportunities execution-history; do
    echo ""
    echo "  Table: $table"
    awslocal dynamodb describe-table --table-name "$table" \
        --query 'Table.{Status:TableStatus,Items:ItemCount,Size:TableSizeBytes,KeySchema:KeySchema}' \
        --output json 2>/dev/null | sed 's/^/    /'
done

echo ""
echo "✓ DynamoDB initialization complete!"
