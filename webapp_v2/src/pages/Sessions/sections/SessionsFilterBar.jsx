import { useEffect, useMemo, useRef, useState } from 'react'
import { CloseButton, Group } from '@mantine/core'
import { ArrowLeftRight, CircleCheckBig, Database, User } from 'lucide-react'
import ValueFilter from '@/components/ValueFilter'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import DatePickerInput from '@/components/DatePickerInput'
import TextInput from '@/components/TextInput'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import { usersService } from '@/services/users'
import { toDateInputValue, toEndOfDayISO, toStartOfDayISO } from '@/utils/datetime'
import SessionIdSearch from './SessionIdSearch'
import WorkflowNavigator from './WorkflowNavigator'
import { REVIEW_STATUS_OPTIONS, TYPE_OPTIONS } from '../constants'

const JIRA_DEBOUNCE_MS = 500

/** Port of `audit-filters` (audit_filters.cljs), widget for widget. */
export default function SessionsFilterBar({ filters, setFilters }) {
  const [users, setUsers] = useState([])
  const roles = usePaginatedConnections({ pageSize: 50 })

  useEffect(() => {
    usersService
      .list()
      .then(({ data }) => setUsers(Array.isArray(data) ? data : []))
      // Non-critical: the rest of the bar works without the user list.
      .catch(() => setUsers([]))
  }, [])

  // The filter sends the user's UUID (matched against s.user_id) but shows their
  // email, exactly as v1 did (audit_filters.cljs:53-55).
  const sortedUsers = useMemo(
    () => [...users].sort((a, b) => (a.name ?? '').localeCompare(b.name ?? '')),
    [users]
  )
  const userEmails = useMemo(
    () => sortedUsers.map((u) => u.email).filter(Boolean),
    [sortedUsers]
  )
  const selectedUserEmail =
    sortedUsers.find((u) => u.id === filters.user)?.email ?? null

  // `usePaginatedConnections` yields { value: connection.id, label: name }, but
  // the gateway matches ?connection= against the connection NAME
  // (gateway/models/session.go). Passing the id writes a UUID and silently
  // returns zero rows — remap so the option value IS the name.
  const roleOptions = useMemo(
    () => roles.options.map((option) => ({ value: option.label, label: option.label })),
    [roles.options]
  )
  const selectedRole = filters.connection
    ? { value: filters.connection, label: filters.connection }
    : null

  // Mantine v8 date inputs speak `YYYY-MM-DD` strings, not Date objects — both
  // for `value` and in `onChange`.
  const dateRange = useMemo(
    () => [toDateInputValue(filters.start_date), toDateInputValue(filters.end_date)],
    [filters.start_date, filters.end_date]
  )

  // Jira is the one free-text filter, so it is the one that needs a debounce.
  // `jiraDraft` is non-null only while the user is mid-typing; the rest of the
  // time the input reads straight off the URL, so Back/forward and deep links
  // are reflected with no mirroring effect. v1 needed an explicit "is the user
  // typing?" guard for the same reason (audit_filters.cljs:111-116).
  const [jiraDraft, setJiraDraft] = useState(null)
  const jiraTimerRef = useRef(null)
  const jira = jiraDraft ?? filters.jira_issue_key ?? ''

  useEffect(() => () => clearTimeout(jiraTimerRef.current), [])

  const commitJira = (next) => {
    clearTimeout(jiraTimerRef.current)
    jiraTimerRef.current = null
    setJiraDraft(null)
    // replace: one history entry per search, not per keystroke.
    setFilters({ jira_issue_key: next.trim() }, { replace: true })
  }

  const handleJiraChange = (next) => {
    setJiraDraft(next)
    clearTimeout(jiraTimerRef.current)
    jiraTimerRef.current = setTimeout(() => commitJira(next), JIRA_DEBOUNCE_MS)
  }

  return (
    <Group gap="sm" wrap="wrap">
      <SessionIdSearch />

      <ValueFilter
        icon={User}
        label="User"
        values={userEmails}
        selected={selectedUserEmail}
        onSelect={(email) =>
          setFilters({ user: sortedUsers.find((u) => u.email === email)?.id ?? '' })
        }
        onClear={() => setFilters({ user: '' })}
      />

      <AsyncValueFilter
        icon={ArrowLeftRight}
        label="Resource Role"
        placeholder="Search resource roles"
        selected={selectedRole}
        onSelect={(option) => setFilters({ connection: option.value })}
        onClear={() => setFilters({ connection: '' })}
        options={roleOptions}
        loading={roles.loading}
        hasMore={roles.hasMore}
        onLoadMore={roles.loadMore}
        searchValue={roles.searchValue}
        onSearchChange={roles.setSearch}
        onOpen={roles.ensureLoaded}
      />

      <ValueFilter
        icon={Database}
        label="Type"
        values={TYPE_OPTIONS}
        selected={filters.type ?? null}
        onSelect={(type) => setFilters({ type })}
        onClear={() => setFilters({ type: '' })}
      />

      <ValueFilter
        icon={CircleCheckBig}
        label="Access Request"
        values={REVIEW_STATUS_OPTIONS}
        selected={filters['review.status'] ?? null}
        onSelect={(status) => setFilters({ 'review.status': status })}
        onClear={() => setFilters({ 'review.status': '' })}
      />

      <DatePickerInput
        type="range"
        placeholder="Period"
        w={220}
        value={dateRange}
        onChange={([start, end]) =>
          setFilters({
            start_date: toStartOfDayISO(start) ?? '',
            end_date: toEndOfDayISO(end) ?? '',
          })
        }
      />

      <TextInput
        w={200}
        placeholder="Jira Ticket ID"
        value={jira}
        onChange={(event) => handleJiraChange(event.currentTarget.value)}
        rightSection={
          jira ? (
            <CloseButton
              size="sm"
              aria-label="Clear Jira ticket filter"
              onClick={() => commitJira('')}
            />
          ) : null
        }
      />

      <WorkflowNavigator />
    </Group>
  )
}
