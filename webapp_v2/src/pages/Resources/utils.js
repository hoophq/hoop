// The type filter of webapp/src/webapp/resources/main.cljs.
export const RESOURCE_TYPES = [
  { value: 'postgres', label: 'PostgreSQL' },
  { value: 'mysql', label: 'MySQL' },
  { value: 'mongodb', label: 'MongoDB' },
  { value: 'mssql', label: 'MSSQL' },
  { value: 'oracledb', label: 'OracleDB' },
  { value: 'ssh', label: 'SSH' },
  { value: 'tcp', label: 'TCP' },
]

// Tag helpers of webapp/src/webapp/connections/views/tag_selector.cljs.
export const tagKeyName = (key) => key.split('/').at(-1)

export function displayKey(key) {
  const parts = tagKeyName(key).split('.')
  return parts.length === 1 ? parts[0] : parts[1]
}

// Tag keys come from users, so a key like `constructor` must not reach Object.prototype.
export function groupTags(tags) {
  const grouped = Object.create(null)
  for (const { key, value } of tags) {
    if (!grouped[key]) grouped[key] = []
    if (!grouped[key].includes(value)) grouped[key].push(value)
  }
  return grouped
}

export const selectedValues = (selected, key) => (Object.hasOwn(selected, key) ? selected[key] : [])

export function countSelected(selected) {
  return Object.values(selected).reduce((sum, values) => sum + values.length, 0)
}

export function tagsToSelector(tags) {
  return Object.entries(tags)
    .flatMap(([key, values]) => values.map((value) => `${key}=${value}`))
    .join(',')
}
