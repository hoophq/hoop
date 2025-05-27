Update the status of a review resource by its resource ID or session ID. This endpoint is used to approve, reject, or revoke reviews for session execution requests.

## Overview

When a user interacts with a session, a review resource is automatically created containing the configured review groups, each initially set to `PENDING` status. **All groups must be approved before the session can be executed.**

The review status updates affect each review group based on the caller's context. Once all groups are `APPROVED`, or if any group becomes `REJECTED` or `REVOKED`, the overall resource status updates accordingly.

## Review Groups

Review groups contain individual review entries that must be completed by authorized users from specific groups. Each entry represents a required approval from a designated reviewer group.

### Initial State

When a review is created, each group entry is populated with the following structure:

```json
{
    "id": "aaa257be-5cc9-401d-ae7e-18ae806d366a",
    "group": "banking",
    "status": "PENDING",
    "reviewed_by": null,
    "review_date": null
}
```

### Completed Review State

After a review is completed, the entry includes the status, review timestamp, and reviewer information:

```json
{
    "id": "a546dfba-d917-4c2b-bc38-7852a7932573",
    "group": "banking",
    "status": "REJECTED",
    "reviewed_by": {
        "id": "17e4ff1a-104c-482c-be68-3c01bfc7028e",
        "name": "John Doe",
        "email": "john.doe@domain.tld",
        "slack_id": ""
    },
    "review_date": "2025-05-27T16:40:05.519754143Z"
}
```

## Review States

### User-Controlled States

These states are set directly by reviewers:

- **`APPROVED`** - The resource has been approved by the reviewer
- **`REJECTED`** - The resource is rejected and cannot be updated further
- **`REVOKED`** - The resource is revoked and cannot be updated further

### System-Controlled States

These states are managed automatically by the gateway:

- **`PENDING`** - Initial state when the review is created
- **`PROCESSING`** - Session is being executed; review cannot be updated
- **`EXECUTED`** - Session completed successfully; review cannot be updated
- **`UNKNOWN`** - Session executed but outcome is indeterminate

## General Rules

### Review Permissions

- Reviews can only be performed when the resource status is `PENDING` or `APPROVED`
- **Resource owners cannot self-approve** - approval requires another member of the same group
- Users are only eligible to review if they are **not the resource owner** or are **administrators**

### Multi-Group Reviews

- If a user belongs to multiple groups, separate review entries are updated for each group
- All group reviews must be completed before session execution

### Status Transitions

- Setting any review to `REJECTED` immediately changes the overall resource status and prevents further updates
- `APPROVED` reviews can still be changed to `REJECTED` or `REVOKED` at any time by the resource owner or administrators
- Once a review reaches `REJECTED` or `REVOKED` the resource is considered as immutable and it cannot be updated again

### Final States

Reviews in `PROCESSING`, `EXECUTED`, or `UNKNOWN` states are immutable and cannot be modified.