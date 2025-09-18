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

# Download file
curl -L \
     -o "$OUTPUT_DIR/$OUTPUT_FILE" \
     --silent \
     --show-error \
     "https://raw.githubusercontent.com/$ORG/$REPO/main/$FILE"

# Check if download was successful
if [ -f "$OUTPUT_DIR/$OUTPUT_FILE" ]; then
    echo "✓ Download complete: $OUTPUT_DIR/$OUTPUT_FILE"
    
    # Validate JSON if jq is installed
    if command -v jq &> /dev/null; then
        if jq empty "$OUTPUT_DIR/$OUTPUT_FILE" 2>/dev/null; then
            CONNECTION_COUNT=$(jq '.connections | length' "$OUTPUT_DIR/$OUTPUT_FILE" 2>/dev/null || echo "0")
            echo "✓ JSON valid (connections: $CONNECTION_COUNT)"
        else
            echo "ERROR: Invalid JSON file"
            exit 1
        fi
    fi
else
    echo "ERROR: Download failed"
    exit 1
fi
