#!/bin/bash

# Configuration
ORG="hoophq"
REPO="documentation"
FILE="store/connections.json"
OUTPUT_DIR="resources/public/data"
OUTPUT_FILE="connections-metadata.json"

echo "Downloading connection metadata..."
echo "  Source: $ORG/$REPO/$FILE"

# Create directory if it doesn't exist
mkdir -p "$OUTPUT_DIR"

HTTP_CODE=$(curl -L \
     -o "$OUTPUT_DIR/$OUTPUT_FILE" \
     -w "%{http_code}" \
     -H "Accept: application/vnd.github.v3.raw" \
     --silent \
     --show-error \
     "https://api.github.com/repos/$ORG/$REPO/contents/$FILE")

echo "HTTP Status: $HTTP_CODE"

# Check if download was successful
if [ -f "$OUTPUT_DIR/$OUTPUT_FILE" ]; then
    FILE_SIZE=$(wc -c < "$OUTPUT_DIR/$OUTPUT_FILE" | tr -d ' ')
    echo "✓ Download complete: $OUTPUT_DIR/$OUTPUT_FILE"
    echo "  File size: $FILE_SIZE bytes"
    
    # Check HTTP status code
    if [ "$HTTP_CODE" -ne 200 ]; then
        echo "⚠ Warning: HTTP Status $HTTP_CODE (expected 200)"
        echo "  Response content:"
        head -n 5 "$OUTPUT_DIR/$OUTPUT_FILE" | sed 's/^/    /'
        
        # Handle specific error codes
        case $HTTP_CODE in
            429)
                echo ""
                echo "ERROR: Rate limit exceeded (429 Too Many Requests)"
                echo "  • GitHub API limit: 60 requests/hour (unauthenticated)"
                ;;
            403)
                echo ""
                echo "ERROR: Forbidden (403)"
                echo "  • Possible rate limit or access restriction"
                ;;
            404)
                echo ""
                echo "ERROR: Not Found (404)"
                echo "  • Check if file path is correct: $FILE"
                echo "  • Repository: $ORG/$REPO"
                ;;
        esac
        exit 1
    fi
    
    # Validate JSON if jq is installed
    if command -v jq &> /dev/null; then
        JQ_ERROR=$(jq empty "$OUTPUT_DIR/$OUTPUT_FILE" 2>&1)
        if [ $? -eq 0 ]; then
            CONNECTION_COUNT=$(jq '.connections | length' "$OUTPUT_DIR/$OUTPUT_FILE" 2>/dev/null || echo "0")
            echo "✓ JSON valid (connections: $CONNECTION_COUNT)"
        else
            echo "ERROR: Invalid JSON file"
            echo "  jq error:"
            echo "$JQ_ERROR" | sed 's/^/    /'
            exit 1
        fi
    else
        echo "⚠ Warning: jq not installed, skipping JSON validation"
    fi
else
    echo "ERROR: Download failed - file not created"
    echo "  Expected: $OUTPUT_DIR/$OUTPUT_FILE"
    exit 1
fi
