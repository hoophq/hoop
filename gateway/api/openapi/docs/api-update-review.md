Update the status of an approval request resource by its resource ID or session ID. This endpoint is used to approve, reject, or revoke approval requests for session execution requests.

## Overview

When a user interacts with a session, an approval request resource is automatically created containing the configured approver groups, each initially set to `PENDING` status. **All groups must be approved before the session can be executed.**

The approval status updates affect each approver group based on the caller's context. Once all groups are `APPROVED`, or if any group becomes `REJECTED` or `REVOKED`, the overall resource status updates accordingly.

## Approver Groups

Approver groups contain individual approval entries that must be completed by authorized users from specific groups. Each entry represents a required approval from a designated approver group.

### Initial State

When an approval request is created, each group entry is populated with the following structure:

```json
{
    "id": "aaa257be-5cc9-401d-ae7e-18ae806d366a",
    "group": "banking",
    "status": "PENDING",
    "reviewed_by": null,
    "review_date": null
}
```

### Completed Approval State

After an approval entry is completed, it includes the status, decision timestamp, and approver information:

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

## Approval States

### User-Controlled States

These states are set directly by approvers:

- **`APPROVED`** - The resource has been approved by the approver
- **`REJECTED`** - The resource is rejected and cannot be updated further
- **`REVOKED`** - The resource is revoked and cannot be updated further

### System-Controlled States

These states are managed automatically by the gateway:

- **`PENDING`** - Initial state when the approval request is created
- **`PROCESSING`** - Session is being executed; approval request cannot be updated
- **`EXECUTED`** - Session completed successfully; approval request cannot be updated
- **`UNKNOWN`** - Session executed but outcome is indeterminate
- **`EXPIRED`** - A sidecar approval request passed its `expires_at` before it was decided or used; nothing was released. Only a control plane sets it

## General Rules

### Approval Permissions

- Approvals can only be decided when the resource status is `PENDING` or `APPROVED`
- **Resource owners cannot self-approve** - approval requires another member of the same group
- Users are only eligible to approve if they are **not the resource owner** or are **administrators**

### Multi-Group Approvals

- If a user belongs to multiple groups, separate approval entries are updated for each group
- All group approvals must be completed before session execution

### Status Transitions

- Setting any approval to `REJECTED` immediately changes the overall resource status and prevents further updates
- `APPROVED` approval requests can still be changed to `REJECTED` at any time by the resource owner or administrators
- `REVOKED` applies only to an `APPROVED` approval request of type `jit`, or to an `APPROVED` approval request a sidecar filed (it has a `listener_name`). A sidecar approval request can be revoked until the sidecar uses the approval; after that it is `EXECUTED` and the request answers `400`
- Once an approval request reaches `REJECTED` or `REVOKED` the resource is considered as immutable and it cannot be updated again
- A decision on a sidecar approval request past its `expires_at` answers `400` (`approval request expired`), and the approval request is `EXPIRED`

### Final States

Approval requests in `PROCESSING`, `EXECUTED`, `UNKNOWN`, or `EXPIRED` states are immutable and cannot be modified.