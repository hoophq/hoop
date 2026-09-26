import { Fragment, useState } from 'react'
import { Divider, Group, Input, Stack, Text, Title } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Accordion from '@/components/Accordion'
import ActionIcon from '@/components/ActionIcon'
import Autocomplete from '@/components/Autocomplete'
import Button from '@/components/Button'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import SectionRow from '@/components/SectionRow'
import SegmentedControl from '@/components/SegmentedControl'
import Select from '@/components/Select'
import Switch from '@/components/Switch'
import TagsInput from '@/components/TagsInput'
import TextInput from '@/components/TextInput'
import { getPath, protocolOptions } from '../listeners'
import { LISTENER_FIELDS, appliesTo } from '../schema'

// A group inside a section. Its title sits between the section title (18px)
// and the field labels (14px/700), or it reads as one more label.
function Block({ title, description, children }) {
  return (
    <Stack gap="md">
      <Stack gap={4}>
        <Title order={5} fw={600}>
          {title}
        </Title>
        {description && (
          <Text size="sm" c="dimmed">
            {description}
          </Text>
        )}
      </Stack>
      {children}
    </Stack>
  )
}

function MapInput({ field, value, onChange, ...wrapper }) {
  // Rows live here so a row whose key is still empty survives a keystroke.
  const [rows, setRows] = useState(() => Object.entries(value ?? {}))
  const commit = (next) => {
    setRows(next)
    const entries = next.filter(([k]) => k.trim())
    onChange(entries.length ? Object.fromEntries(entries) : undefined)
  }
  const setRow = (i, row) => commit(rows.map((r, j) => (j === i ? row : r)))

  return (
    <Input.Wrapper label={field.label} {...wrapper}>
      <Stack gap="xs" mt={4}>
        {rows.map(([k, v], i) => (
          <Group key={i} gap="xs" wrap="nowrap">
            <TextInput
              flex={1}
              placeholder={field.placeholder}
              value={k}
              onChange={(e) => setRow(i, [e.currentTarget.value, v])}
            />
            {field.enum ? (
              <Select
                w={160}
                data={field.enum}
                value={v || null}
                allowDeselect={false}
                onChange={(next) => setRow(i, [k, next])}
              />
            ) : (
              <TextInput w={160} value={v} onChange={(e) => setRow(i, [k, e.currentTarget.value])} />
            )}
            <ActionIcon
              variant="subtle"
              color="gray"
              aria-label="Remove"
              onClick={() => commit(rows.filter((_, j) => j !== i))}
            >
              <Trash2 size={16} />
            </ActionIcon>
          </Group>
        ))}
        <Button
          variant="subtle"
          size="compact-sm"
          w="fit-content"
          leftSection={<Plus size={14} />}
          onClick={() => commit([...rows, ['', field.enum?.[0] ?? '']])}
        >
          Add
        </Button>
      </Stack>
    </Input.Wrapper>
  )
}

// An open enum suggests its values and accepts any other.
function ListInput({ field, value, onChange, ...props }) {
  if (field.enum && !field.open) return <MultiSelect data={field.enum} value={value ?? []} onChange={onChange} {...props} />
  return <TagsInput placeholder={field.placeholder} data={field.enum} value={value ?? []} onChange={onChange} {...props} />
}

// An object's fields. A presence block adds the switch that creates or removes it.
function ObjectBody({ field, path, form, setField, errors }) {
  const inner = <Fields fields={field.fields ?? []} prefix={path} form={form} setField={setField} errors={errors} />
  if (!field.presence) return inner
  const on = getPath(form, path) !== undefined
  return (
    <Stack gap="md">
      <Switch
        label={field.label}
        description={field.help}
        error={errors[path]}
        checked={on}
        onChange={(e) => setField(path, e.currentTarget.checked ? {} : undefined)}
      />
      {on && inner}
    </Stack>
  )
}

// One schema field, with the input its type calls for.
function Field({ field, path, form, setField, errors }) {
  const value = getPath(form, path)
  const set = (v) => setField(path, v)
  const common = { label: field.label, description: field.help, error: errors[path] }

  if (field.type === 'object') {
    const body = <ObjectBody field={field} path={path} form={form} setField={setField} errors={errors} />
    if (field.presence) return body
    return (
      <Block title={field.label} description={field.help}>
        {body}
      </Block>
    )
  }

  if (path === 'protocol') {
    return <Select {...common} required data={protocolOptions(value)} value={value || null} onChange={set} allowDeselect={false} />
  }

  switch (field.type) {
    case 'string':
      if (field.enum && field.open) {
        return (
          <Autocomplete
            {...common}
            required={field.required}
            placeholder={field.placeholder}
            data={field.enum}
            value={value ?? ''}
            onChange={set}
          />
        )
      }
      if (field.enum?.length <= 3) {
        return (
          <Input.Wrapper {...common} required={field.required}>
            <SegmentedControl
              mt={4}
              w="fit-content"
              value={value || field.default || field.enum[0]}
              onChange={set}
              data={field.enum.map((v) => ({ value: v, label: v }))}
            />
          </Input.Wrapper>
        )
      }
      if (field.enum) {
        return <Select {...common} data={field.enum} value={value || field.default || null} onChange={set} allowDeselect={false} />
      }
      return (
        <TextInput
          {...common}
          required={field.required}
          placeholder={field.placeholder}
          value={value ?? ''}
          onChange={(e) => set(e.currentTarget.value)}
        />
      )
    case 'integer':
      return <NumberInput {...common} min={0} value={value ?? 0} onChange={(v) => set(Number(v) || 0)} />
    case 'boolean':
      return <Switch {...common} checked={value === true} onChange={(e) => set(e.currentTarget.checked)} />
    case 'list':
      if (field.presence) {
        const on = value !== undefined
        return (
          <Stack gap="xs">
            <Switch {...common} checked={on} onChange={(e) => set(e.currentTarget.checked ? [] : undefined)} />
            {on && <ListInput field={field} value={value} onChange={set} />}
          </Stack>
        )
      }
      return <ListInput field={field} value={value} onChange={set} {...common} />
    case 'map':
      return <MapInput field={field} value={value} onChange={set} {...common} />
    default:
      return (
        <Input.Wrapper
          label={field.label}
          description={`This form cannot edit a ${field.type} setting yet; its value is kept as it is.`}
        />
      )
  }
}

function Fields({ fields, prefix, form, ...rest }) {
  return (
    <Stack gap="md">
      {fields
        .filter((f) => appliesTo(f, form.protocol))
        .map((f) => {
          const path = prefix ? `${prefix}.${f.key}` : f.key
          return <Field key={path} field={f} path={path} form={form} {...rest} />
        })}
    </Stack>
  )
}

/**
 * The fields of one listener, rendered from the sidecar schema, with no
 * chrome of its own. `form` and `errors` come from ../listeners.
 */
export default function ListenerForm({ form, setField, errors }) {
  const ctx = { form, setField, errors }
  const visible = LISTENER_FIELDS.filter((f) => appliesTo(f, form.protocol))
  const basic = visible.filter((f) => f.basic)
  // A required block (ssh) is part of what makes the listener work: its fields
  // join the Listener section, and its own blocks get a section each.
  const required = visible.filter((f) => !f.basic && f.type === 'object' && f.required)
  const subBlocks = required.flatMap((f) =>
    (f.fields ?? [])
      .filter((c) => c.type === 'object' && appliesTo(c, form.protocol))
      .map((c) => ({ field: c, path: `${f.key}.${c.key}` })),
  )
  const advanced = visible.filter((f) => !f.basic && !required.includes(f))
  const scalars = advanced.filter((f) => f.type !== 'object')
  const blocks = advanced.filter((f) => f.type === 'object')

  return (
    <Stack gap="xxlAlt">
      <SectionRow
        title="Listener"
        description="Where clients reach the sidecar, where the sidecar reaches your resource, and the protocol between them."
      >
        <Stack gap="md">
          <Fields fields={basic} prefix="" {...ctx} />
          {required.map((f) => (
            <Fields key={f.key} fields={(f.fields ?? []).filter((c) => c.type !== 'object')} prefix={f.key} {...ctx} />
          ))}
        </Stack>
      </SectionRow>

      {subBlocks.map(({ field, path }) => (
        <SectionRow key={path} title={field.label} description={field.help}>
          <ObjectBody field={field} path={path} {...ctx} />
        </SectionRow>
      ))}

      {advanced.length > 0 && (
        <Accordion>
          <Accordion.Item value="advanced">
            <Accordion.Control>Advanced</Accordion.Control>
            <Accordion.Panel>
              <Stack gap="lg" pt="xs">
                {scalars.length > 0 && <Fields fields={scalars} prefix="" {...ctx} />}
                {blocks.map((f, i) => (
                  <Fragment key={f.key}>
                    {(i > 0 || scalars.length > 0) && <Divider />}
                    <Field field={f} path={f.key} {...ctx} />
                  </Fragment>
                ))}
              </Stack>
            </Accordion.Panel>
          </Accordion.Item>
        </Accordion>
      )}
    </Stack>
  )
}
