// Recommended Live Data Masking templates for the product activation journey.
//
// Source of truth: live-data-masking-templates.json (Linear, Feature Specs |
// Product Activation Journey / EVL-69).
//
// Contract:
// - `id` is the ?template= deep-link value used by CLJS activation-journey
//   surfaces. Keep ids and `rule.name` in sync with `masking-templates` in
//   webapp/src/webapp/features/activation_journey/templates.cljs — the CLJS
//   side matches configured rules by name to decide card/banner states.
// - `rule` is shaped exactly like a GET /datamasking-rules/:id response so
//   apiRuleToFormRows() consumes it directly to seed the create form.
// - Free-plan constraint: templates must resolve to a single preset row.
//   The free-plan RulesTable locks preset selection to one option and hides
//   the add/remove row controls, so a multi-row template would be silently
//   truncated for free users.
//
// TODO(EVL-69): replace with the content of live-data-masking-templates.json
// (Linear attachment).
export const MASKING_TEMPLATES = [
  {
    id: 'mask-sensitive-field-type',
    title: 'Mask 1 sensitive field type',
    rule: {
      name: 'Mask sensitive fields',
      description:
        'Matches PII in query results and redacts it before the response reaches the client.',
      connection_ids: [],
      attributes: [],
      supported_entity_types: [
        {
          name: 'CONTACT_INFORMATION',
          entity_types: ['EMAIL_ADDRESS', 'PHONE_NUMBER', 'PERSON'],
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
