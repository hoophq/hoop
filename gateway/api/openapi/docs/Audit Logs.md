Security audit log API. Only users in the **admin** group can access these endpoints.

Audit log entries record security-relevant events (who performed an action, when, on which resource, and whether it succeeded). Use the list endpoint with filters to query by actor, resource type, action, outcome, or date range. Results are paginated and ordered by `created_at` descending.
