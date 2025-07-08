# Connection PATCH Endpoints

This document describes the comprehensive PATCH support for individual connection fields that is already implemented in the codebase. This addresses the Slack conversation about the difficulty of updating connection types by providing granular field-level updates.

## Overview

The implementation follows the **session metadata PATCH pattern** as the reference architecture (not data masking rules). All PATCH endpoints:
- Return `204 No Content` on success
- Have proper error handling (400/404/500)  
- Require admin-only access
- Protect against updating agent-managed connections
- Support atomic updates for individual fields

## Available PATCH Endpoints

### 1. Update Connection Command
**Endpoint:** `PATCH /connections/{nameOrID}/command`

Updates the shell command array for the connection.

```bash
curl -X PATCH "https://api.example.com/connections/pgdemo/command" \
  -H "Content-Type: application/json" \
  -d '{
    "command": ["/bin/bash", "-c", "echo hello"]
  }'
```

### 2. Update Connection Type
**Endpoint:** `PATCH /connections/{nameOrID}/type`

Updates the connection type and subtype. This directly addresses the Slack conversation issue.

```bash
curl -X PATCH "https://api.example.com/connections/pgdemo/type" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "custom",
    "subtype": "cloudwatch"
  }'
```

### 3. Update Connection Secrets
**Endpoint:** `PATCH /connections/{nameOrID}/secrets`

Updates the secrets/environment variables for the connection.

```bash
curl -X PATCH "https://api.example.com/connections/pgdemo/secrets" \
  -H "Content-Type: application/json" \
  -d '{
    "secrets": {
      "envvar:DATABASE_URL": "cG9zdGdyZXM6Ly91c2VyOnBhc3N3b3JkQGxvY2FsaG9zdDo1NDMyL2RiCg==",
      "envvar:API_KEY": "_aws:my-secret:api-key"
    }
  }'
```

### 4. Update Connection Reviewers
**Endpoint:** `PATCH /connections/{nameOrID}/reviewers`

Updates the list of reviewer groups for the connection.

```bash
curl -X PATCH "https://api.example.com/connections/pgdemo/reviewers" \
  -H "Content-Type: application/json" \
  -d '{
    "reviewers": ["dba-group", "sre-team"]
  }'
```

### 5. Update Connection Redact Types
**Endpoint:** `PATCH /connections/{nameOrID}/redact-types`

Updates the list of data types to redact in the connection output.

```bash
curl -X PATCH "https://api.example.com/connections/pgdemo/redact-types" \
  -H "Content-Type: application/json" \
  -d '{
    "redact_types": ["EMAIL_ADDRESS", "PHONE_NUMBER", "CREDIT_CARD_NUMBER"]
  }'
```

### 6. Update Legacy Tags
**Endpoint:** `PATCH /connections/{nameOrID}/tags`

Updates the legacy tags for the connection (deprecated in favor of connection tags).

```bash
curl -X PATCH "https://api.example.com/connections/pgdemo/tags" \
  -H "Content-Type: application/json" \
  -d '{
    "tags": ["prod", "database"]
  }'
```

### 7. Update Connection Tags
**Endpoint:** `PATCH /connections/{nameOrID}/connection-tags`

Updates the modern key-value connection tags.

```bash
curl -X PATCH "https://api.example.com/connections/pgdemo/connection-tags" \
  -H "Content-Type: application/json" \
  -d '{
    "connection_tags": {
      "environment": "prod",
      "tier": "backend",
      "team": "banking"
    }
  }'
```

### 8. Update Access Modes
**Endpoint:** `PATCH /connections/{nameOrID}/access-modes`

Updates the access mode settings for the connection.

```bash
curl -X PATCH "https://api.example.com/connections/pgdemo/access-modes" \
  -H "Content-Type: application/json" \
  -d '{
    "access_mode_runbooks": "enabled",
    "access_mode_exec": "enabled", 
    "access_mode_connect": "disabled",
    "access_schema": "enabled"
  }'
```

### 9. Update Guard Rail Rules
**Endpoint:** `PATCH /connections/{nameOrID}/guardrail-rules`

Updates the guard rail rules associated with the connection.

```bash
curl -X PATCH "https://api.example.com/connections/pgdemo/guardrail-rules" \
  -H "Content-Type: application/json" \
  -d '{
    "guardrail_rules": ["5701046A-7B7A-4A78-ABB0-A24C95E6FE54"]
  }'
```

### 10. Update Jira Issue Template
**Endpoint:** `PATCH /connections/{nameOrID}/jira-issue-template`

Updates the Jira issue template associated with the connection.

```bash
curl -X PATCH "https://api.example.com/connections/pgdemo/jira-issue-template" \
  -H "Content-Type: application/json" \
  -d '{
    "jira_issue_template_id": "B19BBA55-8646-4D94-A40A-C3AFE2F4BAFD"
  }'
```

## Response Codes

All PATCH endpoints return the same response codes:

- **204 No Content** - Update successful
- **400 Bad Request** - Invalid request body or agent-managed connection
- **404 Not Found** - Connection not found  
- **500 Internal Server Error** - Server error

## Error Responses

When updating an agent-managed connection:
```json
{
  "message": "unable to update a connection managed by its agent"
}
```

When connection is not found:
```json
{
  "message": "connection not found"
}
```

## Solving the Slack Thread Problem

**Original Problem:** Updating a connection from regular type to `custom/cloudwatch` required using `--overwrite` flag with all existing configuration, which was cumbersome.

**Solution:** Now you can simply use:
```bash
curl -X PATCH "https://api.example.com/connections/my-connection/type" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "custom",
    "subtype": "cloudwatch"
  }'
```

This atomic update only changes the type/subtype fields without requiring knowledge of or modification to any other connection configuration.

## Architecture

The implementation follows these architectural patterns:

1. **Reference Pattern:** Session metadata PATCH (`/sessions/{id}/metadata`)
2. **Atomic Updates:** Each endpoint updates only specific fields
3. **Access Control:** Admin-only endpoints with proper middleware
4. **Validation:** Protection against updating agent-managed connections
5. **Transaction Safety:** Complex updates use database transactions
6. **Consistent Responses:** All return 204 on success

## Implementation Details

- **API Layer:** `gateway/api/connections/connections.go` - All 10 PATCH functions
- **Models Layer:** `gateway/models/connections.go` - Individual field update functions  
- **OpenAPI Types:** `gateway/api/openapi/types.go` - Request type definitions
- **Routes:** `gateway/api/server.go` - Route registration with middleware
- **Pattern:** Follows session `PatchMetadata` architecture

All code is fully implemented, tested, and ready for use.