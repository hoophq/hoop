import api from './api'

export const reportsService = {
  /**
   * Compliance report (admin/auditor-gated).
   *
   * → { overall: { score 0-1000, level, compliant, total_applicable },
   *     categories: [{ id, title, compliant, total }],
   *     action_required: [{ id, title, category, status, message, evidence, action? }],
   *     frameworks: [{ id, name, score_percent, level, breakdown, groups:
   *       [{ id, title, controls: [{ id, title, description, status, message,
   *          evidence, action?: { label, type: 'app'|'docs'|'external', target } }] }] }] }
   */
  getComplianceReport: () => api.get('/reports/compliance').then((res) => res.data),
}
