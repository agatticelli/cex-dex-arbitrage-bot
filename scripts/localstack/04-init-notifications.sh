#!/bin/bash
set -e

echo "=================================================="
echo "Initializing Notification Services (SES, SNS Mobile)"
echo "=================================================="

# Verify SES email identity
echo "Verifying SES email identity..."
awslocal ses verify-email-identity \
    --email-address notifications@arbitrage.bot \
    > /dev/null 2>&1 || echo "  Email identity already verified"

echo "✓ SES email identity verified: notifications@arbitrage.bot"

# Create SNS Platform Application for mobile push (mock)
echo ""
echo "Creating SNS platform application for mobile push..."
awslocal sns create-platform-application \
    --name arbitrage-mobile \
    --platform GCM \
    --attributes "PlatformCredential=mock-api-key" \
    > /dev/null 2>&1 || echo "  Platform application already exists"

echo "✓ SNS mobile platform application created"

# Verification
echo ""
echo "=================================================="
echo "Notification Services Summary"
echo "=================================================="

echo "SES Verified Identities:"
awslocal ses list-identities --output text --query 'Identities[]' 2>/dev/null | sed 's/^/  /' || echo "  None"

echo ""
echo "SNS Platform Applications:"
awslocal sns list-platform-applications --output json 2>/dev/null | \
    jq -r '.PlatformApplications[] | "  \(.PlatformApplicationArn)"' 2>/dev/null || echo "  None"

echo ""
echo "✓ Notification services initialization complete!"
