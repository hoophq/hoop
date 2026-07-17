// Recommended Live Data Masking templates for the product activation journey.
//
// Source of truth: live-data-masking-templates.json (Feature Specs | Product
// Activation Journey / EVL-69), ordered by recommendation: developer access
// to production -> support-team access -> third-party contractor access.
//
// Contract:
// - `id` is the ?template= deep-link value used by CLJS activation-journey
//   surfaces and equals `rule.name`. Keep names in sync with
//   `masking-templates` in
//   webapp/src/webapp/features/activation_journey/templates.cljs — the CLJS
//   side matches configured rules by name to decide card/banner states.
// - `rule` is shaped like a GET /datamasking-rules/:id response so
//   apiRuleToFormRows() consumes it directly to seed the create form.
//
// Adaptations from the handoff JSON (flagged for design/product review):
// - Entity groups ("PII", "Government IDs", ...) are flattened into a single
//   CUSTOM_SELECTION entry per template: the rules table only understands
//   PRESET_DEFINITIONS keys or CUSTOM_SELECTION, and the grouping is
//   cosmetic — enforcement depends only on the entity_types union.
// - Entity types absent from the Presidio list (PRESIDIO_OPTIONS) were
//   dropped from the contractor template: AWS_ACCESS_KEY, AZURE_AUTH_TOKEN,
//   BASIC_AUTH_STRING, GENERIC_API_KEY, GITHUB_TOKEN, PASSWORD,
//   URL_PASSWORD, US_HEALTHCARE_NPI. The masking create flow is gated to
//   the mspresidio provider, and the form cannot represent entities outside
//   that list.
// - The JSON's `attributes` tags (production, pii, ...) are omitted: the
//   Attributes field is populated from org-defined attributes, which may
//   not exist in the target org.
export const MASKING_TEMPLATES = [
  {
    id: 'prod-mask-pii-developer-access',
    title: 'Mask PII for developer access',
    rule: {
      name: 'prod-mask-pii-developer-access',
      description:
        'Mask personal identifiers returned in production query results for developers debugging live issues: names, emails, phone numbers, and physical addresses. Real scenario: an engineer runs SELECT * FROM users to trace a bug and sees thousands of real customer records.',
      connection_ids: [],
      attributes: [],
      score_threshold: 0.6,
      supported_entity_types: [
        {
          name: 'CUSTOM_SELECTION',
          entity_types: ['PERSON', 'EMAIL_ADDRESS', 'PHONE_NUMBER', 'LOCATION'],
        },
      ],
      custom_entity_types: [],
    },
  },
  {
    id: 'support-mask-sensitive-customer-data',
    title: 'Mask sensitive customer data',
    rule: {
      name: 'support-mask-sensitive-customer-data',
      description:
        'Mask sensitive customer identifiers for support teams looking up account records: names, emails, phone numbers, government IDs, and financial data. Real scenario: a support agent searches for a user account and the result includes SSN and credit card columns alongside account status.',
      connection_ids: [],
      attributes: [],
      score_threshold: 0.6,
      supported_entity_types: [
        {
          name: 'CUSTOM_SELECTION',
          entity_types: [
            'PERSON',
            'EMAIL_ADDRESS',
            'PHONE_NUMBER',
            'LOCATION',
            'US_SSN',
            'US_PASSPORT',
            'US_DRIVER_LICENSE',
            'CREDIT_CARD',
            'IBAN_CODE',
            'US_BANK_NUMBER',
          ],
        },
      ],
      custom_entity_types: [],
    },
  },
  {
    id: 'contractor-mask-all-sensitive-data',
    title: 'Mask all sensitive data',
    rule: {
      name: 'contractor-mask-all-sensitive-data',
      description:
        'Apply maximum masking coverage for external contractors accessing production databases: all PII, government IDs, financial data, credentials, and health information. Real scenario: a contractor is granted temporary database access for a migration task and the schema contains customer health records and payment data.',
      connection_ids: [],
      attributes: [],
      score_threshold: 0.5,
      supported_entity_types: [
        {
          name: 'CUSTOM_SELECTION',
          entity_types: [
            'PERSON',
            'EMAIL_ADDRESS',
            'PHONE_NUMBER',
            'LOCATION',
            'US_SSN',
            'US_PASSPORT',
            'US_DRIVER_LICENSE',
            'CREDIT_CARD',
            'IBAN_CODE',
            'US_BANK_NUMBER',
            'MEDICAL_LICENSE',
          ],
        },
      ],
      custom_entity_types: [],
    },
  },
]

export function findMaskingTemplate(id) {
  if (!id) return null
  return MASKING_TEMPLATES.find((template) => template.id === id) ?? null
}
