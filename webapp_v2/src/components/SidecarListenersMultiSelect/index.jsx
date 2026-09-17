import { useEffect, useState } from 'react'
import MultiSelect from '@/components/MultiSelect'
import { sidecarsService } from '@/services/sidecars'

export default function SidecarListenersMultiSelect({
  value = [], // Array of strings: "sidecar_id/listener_name"
  onChange,
  label = 'Sidecar Listeners',
  placeholder = 'Select sidecar listeners...',
  required = false,
}) {
  const [options, setOptions] = useState([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let active = true
    const fetchListeners = async () => {
      try {
        const res = await sidecarsService.list()
        if (!active) return
        const opts = []
        ;(res.data || []).forEach((sc) => {
          const listeners = sc.configuration?.listeners ?? []
          listeners.forEach((l) => {
            opts.push({
              value: `${sc.id}/${l.name}`,
              label: `${sc.name} > ${l.name}`,
            })
          })
        })
        setOptions(opts)
      } catch (err) {
        // Fall back gracefully
      } finally {
        setLoading(false)
      }
    }
    fetchListeners()
    return () => {
      active = false
    }
  }, [])

  return (
    <MultiSelect
      label={label}
      placeholder={loading ? 'Loading sidecar listeners...' : placeholder}
      required={required}
      value={value}
      onChange={onChange}
      data={options}
      searchable
    />
  )
}
