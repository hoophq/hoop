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

export function groupTags(tags) {
  const grouped = {}
  for (const { key, value } of tags) {
    if (!grouped[key]) grouped[key] = []
    if (!grouped[key].includes(value)) grouped[key].push(value)
  }
  return grouped
}

export function countSelected(selected) {
  return Object.values(selected).reduce((sum, values) => sum + values.length, 0)
}

export function tagsToSelector(tags) {
  return Object.entries(tags)
    .flatMap(([key, values]) => values.map((value) => `${key}=${value}`))
    .join(',')
}
