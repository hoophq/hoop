import { useEffect, useRef, useState } from 'react'
import { CloseButton } from '@mantine/core'
import { Search } from 'lucide-react'
import TextInput from '@/components/TextInput'
import { showSnackbar } from '@/utils/snackbar'
import { useSessionsStore } from '../store'
import { MAX_LOOKUP_IDS } from '../constants'

const DEBOUNCE_MS = 500

const parseIds = (raw) =>
  raw
    .split(',')
    .map((id) => id.trim())
    .filter(Boolean)

/**
 * Port of the session-ID search box (audit_filters.cljs:118-134).
 *
 * It hijacks the whole list: while it has results the page renders them instead
 * of the filtered list and hides pagination (main.cljs:45-52). The ID list is
 * deliberately NOT written to the query string — v1 kept it in a local atom,
 * `GET /sessions` has no `id` param (this is N detail fetches), and a URL `?id=`
 * means the separate /sessions/filtered surface.
 */
export default function SessionIdSearch() {
  const [value, setValue] = useState('')
  const timerRef = useRef(null)
  const lookupByIds = useSessionsStore((s) => s.lookupByIds)
  const clearLookup = useSessionsStore((s) => s.clearLookup)

  const cancelPending = () => {
    if (timerRef.current) {
      clearTimeout(timerRef.current)
      timerRef.current = null
    }
  }

  useEffect(() => {
    return () => {
      cancelPending()
      clearLookup()
    }
  }, [clearLookup])

  const run = (raw) => {
    const ids = parseIds(raw)
    if (!ids.length) {
      clearLookup()
      return
    }
    if (ids.length > MAX_LOOKUP_IDS) {
      // Each id costs one request; an unbounded paste would fan out to hundreds
      // of parallel GETs from a single keystroke.
      showSnackbar({
        level: 'error',
        text: `Too many session IDs.`,
        description: `Search up to ${MAX_LOOKUP_IDS} IDs at a time — ${ids.length} were provided.`,
      })
      return
    }
    lookupByIds(ids)
  }

  const handleChange = (raw) => {
    setValue(raw)
    cancelPending()
    if (!raw.trim()) {
      clearLookup()
      return
    }
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      run(raw)
    }, DEBOUNCE_MS)
  }

  const clear = () => {
    cancelPending()
    setValue('')
    clearLookup()
  }

  return (
    <TextInput
      w={310}
      placeholder="Search by IDs (separated by comma)"
      value={value}
      onChange={(event) => handleChange(event.currentTarget.value)}
      onKeyDown={(event) => {
        if (event.key !== 'Enter') return
        // v1 fired immediately on Enter AND left the debounce running, costing
        // 2N requests. Cancel first.
        cancelPending()
        run(value)
      }}
      leftSection={<Search size={16} />}
      rightSection={
        value ? <CloseButton size="sm" onClick={clear} aria-label="Clear ID search" /> : null
      }
    />
  )
}
