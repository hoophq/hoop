import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Popover, Stack } from '@mantine/core'
import { Workflow } from 'lucide-react'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'

/**
 * Port of the Workflow popover (audit_filters.cljs:415-440).
 *
 * Despite sitting in the filter bar this is NOT a filter — it never touches the
 * query string or `GET /sessions`. It navigates to `/workflows/:correlation-id`,
 * which is still a CLJS route (B2.2). Kept as a navigator deliberately, matching
 * v1; the gateway does accept a `correlation_id` list filter, but v1 never used
 * it and the per-row workflow chip already covers the same destination.
 */
export default function WorkflowNavigator() {
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [value, setValue] = useState('')

  const submit = () => {
    const trimmed = value.trim()
    if (!trimmed) return
    setOpen(false)
    navigate(`/workflows/${encodeURIComponent(trimmed)}`)
  }

  return (
    <Popover
      opened={open}
      onChange={setOpen}
      position="bottom-start"
      width={320}
      withinPortal
    >
      <Popover.Target>
        <Button
          variant="default"
          color="gray"
          leftSection={<Workflow size={16} />}
          onClick={() => {
            setValue('')
            setOpen((previous) => !previous)
          }}
        >
          Workflow
        </Button>
      </Popover.Target>
      <Popover.Dropdown p="xs">
        <Stack gap="xs">
          <TextInput
            placeholder="Enter a correlation ID"
            value={value}
            onChange={(event) => setValue(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') submit()
            }}
            leftSection={<Workflow size={14} />}
          />
          <Button fullWidth disabled={!value.trim()} onClick={submit}>
            Open Workflow
          </Button>
        </Stack>
      </Popover.Dropdown>
    </Popover>
  )
}
