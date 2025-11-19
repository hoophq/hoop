#!/bin/bash

# Simple SSM Connection Creation Script
# You should run hoop login before running this script

# Default values
HOOP_API_URL="${HOOP_API_URL:-http://localhost:8009}"
CONNECTION_NAME="${CONNECTION_NAME:-ssm-connection}"
AGENT_ID="${AGENT_ID:-75122BCE-F957-49EB-A812-2AB60977CD9F}" # set the default docker agent id
AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-}"
AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-}"
AWS_REGION="${AWS_REGION:-us-east-1}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -u, --url URL              Hoop API URL (default: http://localhost:8009)"
    echo "  -n, --name NAME            Connection name (default: ssm-connection)"
    echo "  -a, --agent-id ID          Agent ID"
    echo "  -k, --access-key KEY       AWS Access Key ID (required)"
    echo "  -s, --secret-key SECRET    AWS Secret Access Key (required)"
    echo "  -r, --region REGION        AWS Region (default: us-east-1)"
    echo "  --help                     Show this help"
    echo ""
    echo "Examples:"
    echo "  $0 -n 'my-ssm-connection' -k 'AKIAIOSFODNN7EXAMPLE' -s 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY'"
    echo "  $0 -n 'prod-ssm' -k 'AKIAIOSFODNN7EXAMPLE' -s 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY' -r 'eu-west-1'"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -u|--url) HOOP_API_URL="$2"; shift 2 ;;
        -n|--name) CONNECTION_NAME="$2"; shift 2 ;;
        -a|--agent-id) AGENT_ID="$2"; shift 2 ;;
        -k|--access-key) AWS_ACCESS_KEY_ID="$2"; shift 2 ;;
        -s|--secret-key) AWS_SECRET_ACCESS_KEY="$2"; shift 2 ;;
        -r|--region) AWS_REGION="$2"; shift 2 ;;
        --help) usage; exit 0 ;;
        *) echo -e "${RED}Unknown option: $1${NC}"; usage; exit 1 ;;
    esac
done

# Check required parameters
if [ -z "$AWS_ACCESS_KEY_ID" ]; then
    echo -e "${RED}❌ Error: AWS Access Key ID is required${NC}"
    echo "Use -k/--access-key option to specify the access key"
    exit 1
fi

if [ -z "$AWS_SECRET_ACCESS_KEY" ]; then
    echo -e "${RED}❌ Error: AWS Secret Access Key is required${NC}"
    echo "Use -s/--secret-key option to specify the secret key"
    exit 1
fi

# Check required tools
if ! command -v hoop &> /dev/null; then
    echo -e "${RED}❌ Error: hoop command not found${NC}"
    exit 1
fi

if ! command -v base64 &> /dev/null; then
    echo -e "${RED}❌ Error: base64 command not found${NC}"
    exit 1
fi

echo -e "${YELLOW}🔐 Running hoop config view token...${NC}"
TOKEN=$(hoop config view token)

if [ $? -ne 0 ] || [ -z "$TOKEN" ]; then
    echo -e "${RED}❌ Hoop login failed${NC}"
    exit 1
fi
echo -e "${GREEN}✅ Login successful${NC}"

# Use the API URL from environment or default
API_URL="$HOOP_API_URL"

# Encode credentials
ENCODED_ACCESS_KEY=$(echo -n "$AWS_ACCESS_KEY_ID" | base64 -w 0)
ENCODED_SECRET_KEY=$(echo -n "$AWS_SECRET_ACCESS_KEY" | base64 -w 0)
ENCODED_REGION=$(echo -n "$AWS_REGION" | base64 -w 0)

# Create SSM connection
echo -e "${YELLOW}Creating SSM connection: $CONNECTION_NAME${NC}"
echo -e "${YELLOW}AWS Region: $AWS_REGION${NC}"

RESPONSE=$(curl --http1.1 -ks -w "\n%{http_code}" \
    -X POST \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{
        \"name\": \"$CONNECTION_NAME\",
        \"type\": \"custom\",
        \"subtype\": \"aws-ssm\",
        \"agent_id\": \"$AGENT_ID\",
        \"command\": [\"aws-ssm.sh\"],
        \"secret\": {
            \"envvar:AWS_ACCESS_KEY_ID\": \"$ENCODED_ACCESS_KEY\",
            \"envvar:AWS_SECRET_ACCESS_KEY\": \"$ENCODED_SECRET_KEY\",
            \"envvar:AWS_REGION\": \"$ENCODED_REGION\"
        },
        \"access_mode_connect\": \"enabled\",
        \"access_mode_exec\": \"enabled\",
        \"access_mode_runbooks\": \"enabled\",
        \"access_schema\": \"disabled\",
        \"redact_enabled\": true,
        \"redact_types\": [],
        \"tags\": [],
        \"guardrail_rules\": [],
        \"connection_tags\": {},
        \"reviewers\": null,
        \"jira_issue_template_id\": null
    }" \
    "$API_URL/api/connections")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
RESPONSE_BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" -eq 201 ]; then
    echo -e "${GREEN}✅ SSM connection created successfully!${NC}"
    if command -v jq &> /dev/null; then
        echo -e "${GREEN}Connection ID: $(echo "$RESPONSE_BODY" | jq -r '.id')${NC}"
        echo -e "${GREEN}Status: $(echo "$RESPONSE_BODY" | jq -r '.status')${NC}"
    fi
elif [ "$HTTP_CODE" -eq 409 ]; then
    echo -e "${RED}❌ Connection '$CONNECTION_NAME' already exists${NC}"
elif [ "$HTTP_CODE" -eq 401 ]; then
    echo -e "${RED}❌ Unauthorized - Check your credentials${NC}"
else
    echo -e "${RED}❌ Error: HTTP $HTTP_CODE${NC}"
    echo "$RESPONSE_BODY"
fi
