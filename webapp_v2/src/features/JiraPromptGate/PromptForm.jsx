import { Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import PaginatedSelect from '@/components/PaginatedSelect'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import { useJiraPromptStore } from '@/stores/useJiraPromptStore'
import { isRequired } from './jiraFields'

const REQUIRED = 'Required'

// One prompt of the template: text (the default), select or datetime-local.
function PromptField({ prompt, value, error, onChange }) {
  const common = {
    label: prompt.label,
    required: isRequired(prompt),
    error,
  }
  if (prompt.field_type === 'select') {
    return (
      <Select
        {...common}
        placeholder="Select an option"
        data={prompt.field_options ?? []}
        value={value ?? null}
        onChange={(v) => onChange(v ?? '')}
        allowDeselect={false}
      />
    )
  }
  if (prompt.field_type === 'datetime-local') {
    return (
      <TextInput
        {...common}
        type="datetime-local"
        value={value ?? ''}
        onChange={(e) => onChange(e.currentTarget.value)}
      />
    )
  }
  return (
    <TextInput
      {...common}
      placeholder={prompt.label}
      value={value ?? ''}
      onChange={(e) => onChange(e.currentTarget.value)}
    />
  )
}

// One CMDB row: the Jira Assets objects its field accepts.
function CmdbField({ row, pick, error }) {
  const pickCmdb = useJiraPromptStore((s) => s.pickCmdb)
  const setSearch = useJiraPromptStore((s) => s.setSearch)
  const loadMore = useJiraPromptStore((s) => s.loadMore)

  return (
    <PaginatedSelect
      label={row.label}
      required={isRequired(row)}
      error={error}
      value={pick?.value ?? null}
      selectedLabel={pick?.label ?? null}
      onChange={(option) => pickCmdb(row.jira_field, option)}
      options={row.jira_values.map((v) => ({ value: v.id, label: v.name }))}
      loading={row.loading}
      hasMore={row.hasMore}
      onLoadMore={() => loadMore(row.jira_field)}
      searchValue={row.search}
      onSearchChange={(term) => setSearch(row.jira_field, term)}
    />
  )
}

export default function PromptForm({ onSubmit, onCancel }) {
  const prompts = useJiraPromptStore((s) => s.prompts)
  const rows = useJiraPromptStore((s) => s.rows)
  const fields = useJiraPromptStore((s) => s.fields)
  const missing = useJiraPromptStore((s) => s.missing)
  const setAnswer = useJiraPromptStore((s) => s.setAnswer)
  const submit = useJiraPromptStore((s) => s.submit)

  const handleSubmit = (event) => {
    event.preventDefault()
    const body = submit()
    if (body) onSubmit(body)
  }

  const errorOf = (jiraField) => (missing.includes(jiraField) ? REQUIRED : undefined)

  return (
    // noValidate: the store checks the required fields, the CMDB pickers included.
    <form onSubmit={handleSubmit} noValidate>
      <Stack gap="lg">
        <Text fz="sm" c="dimmed">
          Fill out following information to proceed with your command request.
        </Text>
        <Stack gap="md">
          {prompts.map((prompt) => (
            <PromptField
              key={prompt.jira_field}
              prompt={prompt}
              value={fields[prompt.jira_field]}
              error={errorOf(prompt.jira_field)}
              onChange={(value) => setAnswer(prompt.jira_field, value)}
            />
          ))}
          {rows.map((row) => (
            <CmdbField
              key={row.jira_field}
              row={row}
              pick={fields[row.jira_field]}
              error={errorOf(row.jira_field)}
            />
          ))}
        </Stack>
        <Group justify="flex-end" gap="sm">
          <Button variant="default" onClick={onCancel}>
            Cancel
          </Button>
          <Button type="submit">Send</Button>
        </Group>
      </Stack>
    </form>
  )
}
