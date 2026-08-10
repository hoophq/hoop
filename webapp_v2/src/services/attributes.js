import api from './api'

const PAGE_SIZE = 100 // gateway cap for /attributes

export const attributesService = {
  // Paginated: defaults to page 1 with 50 rows, `page_size` caps at 100.
  list: (params) => api.get('/attributes', { params }),
  // Every attribute, walked page by page. Pickers that drive a full-replace
  // write need the complete set — a truncated list would let an admin silently
  // drop associations they never saw.
  listAll: async () => {
    const all = []
    for (let page = 1; ; page += 1) {
      const { data } = await attributesService.list({ page, page_size: PAGE_SIZE })
      const rows = data?.data ?? []
      all.push(...rows)
      if (rows.length === 0 || all.length >= (data?.pages?.total ?? all.length)) {
        return all
      }
    }
  },
  // Attribute names are user-defined, so every path segment is encoded.
  get: (name) => api.get(`/attributes/${encodeURIComponent(name)}`),
  create: (data) => api.post('/attributes', data),
  update: (name, data) => api.put(`/attributes/${encodeURIComponent(name)}`, data),
  remove: (name) => api.delete(`/attributes/${encodeURIComponent(name)}`),
}
