#!/bin/bash

# Simple RDP Credentials Creation Script
# Creates credentials for an existing RDP connection
# You should run hoop login before running this script

# Default values
HOOP_API_URL="${HOOP_API_URL:-http://localhost:8009}"
CONNECTION_NAME="${CONNECTION_NAME:-}"
ACCESS_DURATION="${ACCESS_DURATION:-3600}"  # 1 hour default

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
    echo "  -n, --name NAME            Connection name (required)"
    echo "  -d, --duration SECONDS     Access duration in seconds (default: 3600)"
    echo "  --help                     Show this help"
    echo ""
    echo "Examples:"
    echo "  $0 -n 'rdpchico2' -d 7200"
    echo "  $0 -n 'my-rdp-server' -d 1800"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -u|--url) HOOP_API_URL="$2"; shift 2 ;;
        -n|--name) CONNECTION_NAME="$2"; shift 2 ;;
        -d|--duration) ACCESS_DURATION="$2"; shift 2 ;;
        --help) usage; exit 0 ;;
        *) echo -e "${RED}Unknown option: $1${NC}"; usage; exit 1 ;;
    esac
done

# Check required parameters
if [ -z "$CONNECTION_NAME" ]; then
    echo -e "${RED}❌ Error: Connection name is required${NC}"
    echo "Use -n/--name option to specify the connection name"
    exit 1
fi

# Check required tools
if ! command -v hoop &> /dev/null; then
    echo -e "${RED}❌ Error: hoop command not found${NC}"
    exit 1
fi

TOKEN=$(hoop config view token)
if [ $? -ne 0 ] || [ -z "$TOKEN" ]; then
    echo -e "${RED}❌ Hoop login failed${NC}"
    exit 1
fi
echo -e "${GREEN}✅ Login successful${NC}"

# Create RDP credentials
echo -e "${YELLOW}Creating credentials for connection: $CONNECTION_NAME${NC}"
echo -e "${YELLOW}Access duration: $ACCESS_DURATION seconds${NC}"

RESPONSE=$(curl -s -w "\n%{http_code}" \
    -X POST \
    -H "Content-Type: application/json" \
    -H "Accept: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{
        \"access_duration_seconds\": $ACCESS_DURATION
    }" \
    "$HOOP_API_URL/api/connections/$CONNECTION_NAME/credentials")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
RESPONSE_BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" -eq 201 ]; then
    echo -e "${GREEN}✅ RDP credentials created successfully!${NC}"
    if command -v jq &> /dev/null; then
        echo -e "${GREEN}Credential ID: $(echo "$RESPONSE_BODY" | jq -r '.id')${NC}"
        echo -e "${GREEN}Connection: $(echo "$RESPONSE_BODY" | jq -r '.connection_name')${NC}"
        echo -e "${GREEN}Type: $(echo "$RESPONSE_BODY" | jq -r '.connection_type')${NC}"
        echo -e "${GREEN}Expires: $(echo "$RESPONSE_BODY" | jq -r '.expire_at')${NC}"
        echo ""
        echo -e "${YELLOW}RDP Connection Info:${NC}"
        echo "$RESPONSE_BODY" | jq '.connection_credentials'
    else
        echo "$RESPONSE_BODY"
    fi
elif [ "$HTTP_CODE" -eq 404 ]; then
    echo -e "${RED}❌ Connection '$CONNECTION_NAME' not found${NC}"
elif [ "$HTTP_CODE" -eq 400 ]; then
    echo -e "${RED}❌ Bad request - Check connection settings${NC}"
    echo "$RESPONSE_BODY"
elif [ "$HTTP_CODE" -eq 401 ]; then
    echo -e "${RED}❌ Unauthorized - Check your credentials${NC}"
else
    echo -e "${RED}❌ Error: HTTP $HTTP_CODE${NC}"
    echo "$RESPONSE_BODY"
fi
