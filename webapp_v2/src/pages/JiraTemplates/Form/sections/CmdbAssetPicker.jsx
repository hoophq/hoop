import { useEffect, useState } from 'react'
import { useDebouncedValue } from '@mantine/hooks'
import Autocomplete from '@/components/Autocomplete'
import { jiraTemplatesService } from '@/services/jiraTemplates'

const QUERY_SEPARATOR = '\u0000'

/**
 * Value picker for a CMDB rule row. Free-typing input that suggests Jira asset
 * objects (searched by name) once the row has an Object Type ID. The stored
 * value is the asset object NAME — the runtime prompt flow resolves it to the
 * object id when the Jira issue is created. If the lookup fails (integration
 * disabled, invalid type id), the field keeps working as plain text.
 */
export default function CmdbAssetPicker({ row, onPatch }) {
  const [options, setOptions] = useState([])

  const queryKey = [row.jira_object_type, row.jira_object_schema_id, row.value].join(
    QUERY_SEPARATOR,
  )
  const [debouncedKey] = useDebouncedValue(queryKey, 300)

  useEffect(() => {
    const [objectTypeId, objectSchemaId, ...nameParts] =
      debouncedKey.split(QUERY_SEPARATOR)
    const name = nameParts.join(QUERY_SEPARATOR)
    // Rendered only while the row has an Object Type ID; the id can still be
    // blank here for one debounce tick after it is cleared — just skip.
    if (!objectTypeId) return undefined
    let cancelled = false
    jiraTemplatesService
      .searchAssetObjects({ objectTypeId, objectSchemaId, name, limit: 50 })
      .then((data) => {
        if (cancelled) return
        const names = (data?.values ?? []).map((v) => v.name).filter(Boolean)
        setOptions([...new Set(names)])
      })
      .catch(() => {
        if (!cancelled) setOptions([])
      })
    return () => {
      cancelled = true
    }
  }, [debouncedKey])

  return (
    <Autocomplete
      placeholder="e.g. value_123"
      data={options}
      value={row.value}
      onChange={(value) => onPatch({ value })}
      comboboxProps={{ withinPortal: true }}
      aria-label="CMDB value"
    />
  )
}
