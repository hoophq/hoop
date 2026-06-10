import api from './api'

export const reviewsService = {
  // data: { status: 'APPROVED' | 'REJECTED' | 'REVOKED', rejection_reason? }
  update: (id, data) => api.put(`/reviews/${id}`, data),
}
