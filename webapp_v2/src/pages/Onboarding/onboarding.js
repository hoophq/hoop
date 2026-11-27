export const SETUP_PATH = '/onboarding/setup'
export const PROTECTION_RULES_PATH = '/onboarding/protection-rules'
export const RESOURCE_SETUP_PATH = '/onboarding/setup/resource'
export const DEMO_NAME = 'postgres-demo'

// The web terminal reads its draft from this key: CLJS writes it with the keyword :code-tmp-db.
export const TERMINAL_DRAFT_KEY = ':code-tmp-db'

export const DEMO_QUERY = `SELECT c.firstname, c.lastname, o.orderid, o.orderdate, SUM(ol.quantity) AS total_quantity, SUM(ol.quantity * p.price) AS total_amount
FROM customers c
JOIN orders o ON c.customerid = o.customerid
JOIN orderlines ol ON o.orderid = ol.orderid
JOIN products p ON ol.prod_id = p.prod_id
WHERE c.country = 'US'
GROUP BY c.firstname, c.lastname, o.orderid, o.orderdate
ORDER BY total_amount DESC;`

// :onboarding/check-user: only an admin of an org with no connection onboards.
export function onboardingPath({ isAdmin, hasConnections, protectionProfile }) {
  if (!isAdmin || hasConnections) return '/client'
  if (protectionProfile == null) return PROTECTION_RULES_PATH
  return SETUP_PATH
}

// connection-postgres-demo in CLJS, without its empty values: a read-only demo database.
export function demoConnection({ agentId, profileAttribute }) {
  return {
    name: DEMO_NAME,
    type: 'database',
    subtype: 'postgres',
    agent_id: agentId,
    access_mode_runbooks: 'enabled',
    access_mode_exec: 'enabled',
    access_mode_connect: 'enabled',
    access_schema: 'enabled',
    reviewers: [],
    redact_enabled: true,
    redact_types: [],
    command: [],
    secret: {
      'envvar:DB': 'ZGVsbHN0b3Jl',
      'envvar:HOST': 'ZGVtby1wZy1kYi5jaDcwN3JuYWl6amcudXMtZWFzdC0xLnJkcy5hbWF6b25hd3MuY29t',
      'envvar:INSECURE': 'ZmFsc2U=',
      'envvar:PASS': 'ZG9sbGFyLW1hbmdlci1jYXJvdXNlLUhFQVJURUQ=',
      'envvar:PORT': 'NTQzMg==',
      'envvar:USER': 'ZGVtb3JlYWRvbmx5',
    },
    ...(profileAttribute ? { attributes: [profileAttribute] } : {}),
  }
}
