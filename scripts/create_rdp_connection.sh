#!/bin/bash

# Simple RDP Connection Creation Script
# Always runs hoop login and creates an RDP connection

# Default values
HOOP_API_URL="${HOOP_API_URL:-http://localhost:8009}"
CONNECTION_NAME="${CONNECTION_NAME:-rdp-connection}"
AGENT_ID="${AGENT_ID:-75122BCE-F957-49EB-A812-2AB60977CD9F}" # set the default docker agent id
RDP_HOST="${RDP_HOST:-0.0.0.0:3389}"
RDP_USERNAME="${RDP_USERNAME:-test}"
RDP_PASSWORD="${RDP_PASSWORD:-test}"
COMMAND="${COMMAND:-bash}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -u, --url URL              Hoop API URL (default: http://localhost:8080)"
    echo "  -n, --name NAME            Connection name (default: rdp-connection)"
    echo "  -a, --agent-id ID          Agent ID"
    echo "  -h, --host HOST:PORT       RDP host:port (default: 0.0.0.0:3389)"
    echo "  -U, --username USERNAME    RDP username (default: test)"
    echo "  -P, --password PASSWORD    RDP password (default: test)"
    echo "  -c, --command COMMAND      Command to execute (default: bash)"
    echo "  --help                     Show this help"
    echo ""
    echo "Examples:"
    echo "  $0 -n 'my-rdp' -h '192.168.1.100:3389' -U 'admin' -P 'password123'"
    echo "  $0 -n 'prod-rdp' -h 'prod-server.com:3389'"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -u|--url) HOOP_API_URL="$2"; shift 2 ;;
        -n|--name) CONNECTION_NAME="$2"; shift 2 ;;
        -a|--agent-id) AGENT_ID="$2"; shift 2 ;;
        -h|--host) RDP_HOST="$2"; shift 2 ;;
        -U|--username) RDP_USERNAME="$2"; shift 2 ;;
        -P|--password) RDP_PASSWORD="$2"; shift 2 ;;
        -c|--command) COMMAND="$2"; shift 2 ;;
        --help) usage; exit 0 ;;
        *) echo -e "${RED}Unknown option: $1${NC}"; usage; exit 1 ;;
    esac
done

# Check required tools
if ! command -v hoop &> /dev/null; then
    echo -e "${RED}❌ Error: hoop command not found${NC}"
    exit 1
fi

if ! command -v base64 &> /dev/null; then
    echo -e "${RED}❌ Error: base64 command not found${NC}"
    exit 1
fi

# Run hoop login and capture token
echo -e "${YELLOW}🔐 Running hoop login...${NC}"
TOKEN=$(hoop login)
if [ $? -ne 0 ] || [ -z "$TOKEN" ]; then
    echo -e "${RED}❌ Hoop login failed${NC}"
    exit 1
fi
echo -e "${GREEN}✅ Login successful${NC}"

# Use the API URL from environment or default
API_URL="$HOOP_API_URL"

# Encode credentials
ENCODED_HOST=$(echo -n "$RDP_HOST" | base64 -w 0)
ENCODED_USERNAME=$(echo -n "$RDP_USERNAME" | base64 -w 0)
ENCODED_PASSWORD=$(echo -n "$RDP_PASSWORD" | base64 -w 0)

# Create RDP connection
echo -e "${YELLOW}Creating RDP connection: $CONNECTION_NAME${NC}"
echo -e "${YELLOW}Host: $RDP_HOST${NC}"
echo -e "${YELLOW}Username: $RDP_USERNAME${NC}"

RESPONSE=$(curl -s -w "\n%{http_code}" \
    -X POST \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{
        \"name\": \"$CONNECTION_NAME\",
        \"type\": \"custom\",
        \"subtype\": \"rdp\",
        \"agent_id\": \"$AGENT_ID\",
        \"command\": [\"$COMMAND\"],
        \"secret\": {
            \"envvar:HOST\": \"$ENCODED_HOST\",
            \"envvar:USER\": \"$ENCODED_USERNAME\",
            \"envvar:PASS\": \"$ENCODED_PASSWORD\"
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
    echo -e "${GREEN}✅ RDP connection created successfully!${NC}"
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
