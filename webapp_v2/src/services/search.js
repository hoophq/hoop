import api from './api'

export function searchAll(term) {
  return api.get('/search', { params: { term } })
}
