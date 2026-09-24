import { Fragment, useState } from 'react'
import { Divider, Group, Input, Stack, Text } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Accordion from '@/components/Accordion'
import ActionIcon from '@/components/ActionIcon'
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
import { ALL_SUPPORTED, LISTENER_FIELDS, appliesTo } from '../schema'

// Inside Advanced the fields keep their own labels: the accordion is already
// one visual block, and a second 2/5 grid nested in it would indent twice.
function Block({ title, description, children }) {
  return (
    <Stack gap="sm">
      <Stack gap={2}>
        <Text fw={600} size="sm">
          {title}
        </Text>
        {description && (
          <Text size="xs" c="dimmed">
            {description}
          </Text>
        )}
      </Stack>
      {children}
    </Stack>
  )
}

const isBlank = (v) => v === undefined || v === '' || v === false || (Array.isArray(v) && v.length === 0)

function MapInput({ field, value, onChange, disabled, ...wrapper }) {
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
              disabled={disabled}
              onChange={(e) => setRow(i, [e.currentTarget.value, v])}
            />
            {field.enum ? (
              <Select
                w={160}
                data={field.enum}
                value={v || null}
                disabled={disabled}
                allowDeselect={false}
                onChange={(next) => setRow(i, [k, next])}
              />
            ) : (
              <TextInput w={160} value={v} disabled={disabled} onChange={(e) => setRow(i, [k, e.currentTarget.value])} />
            )}
            <ActionIcon
              variant="subtle"
              color="gray"
              aria-label="Remove"
              disabled={disabled}
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
          disabled={disabled}
          onClick={() => commit([...rows, ['', field.enum?.[0] ?? '']])}
        >
          Add
        </Button>
      </Stack>
    </Input.Wrapper>
  )
}

function ListInput({ field, value, onChange, ...props }) {
  if (field.enum) return <MultiSelect data={field.enum} value={value ?? []} onChange={onChange} {...props} />
  return <TagsInput placeholder={field.placeholder} value={value ?? []} onChange={onChange} {...props} />
}

/**
 * One schema field. A key the sidecar did not report is disabled while it is
 * empty, and stays editable when set so the operator can clear it.
 */
function Field({ field, path, form, setField, errors, support }) {
  const value = getPath(form, path)
  const set = (v) => setField(path, v)
  const supported = support.key(path)
  const description = supported ? field.help : [field.help, 'Not supported by this sidecar.'].filter(Boolean).join(' ')
  const common = { label: field.label, description, error: errors[path], disabled: !supported && isBlank(value) }

  if (field.type === 'object') {
    const inner = <Fields fields={field.fields ?? []} prefix={path} form={form} setField={setField} errors={errors} support={support} />
    if (!field.presence) {
      return (
        <Block title={field.label} description={description}>
          {inner}
        </Block>
      )
    }
    const on = value !== undefined
    return (
      <Stack gap="md">
        <Switch {...common} disabled={!supported && !on} checked={on} onChange={(e) => set(e.currentTarget.checked ? {} : undefined)} />
        {on && inner}
      </Stack>
    )
  }

  if (path === 'protocol') {
    return (
      <Select
        {...common}
        required
        data={protocolOptions(value, support)}
        value={value || null}
        onChange={set}
        allowDeselect={false}
      />
    )
  }

  switch (field.type) {
    case 'string':
      if (field.enum?.length <= 3) {
        return (
          <Input.Wrapper {...common} required={field.required}>
            <SegmentedControl
              mt={4}
              w="fit-content"
              value={value || field.default || field.enum[0]}
              onChange={set}
              disabled={common.disabled}
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
            <Switch {...common} disabled={!supported && !on} checked={on} onChange={(e) => set(e.currentTarget.checked ? [] : undefined)} />
            {on && <ListInput field={field} value={value} onChange={set} disabled={common.disabled} />}
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
 * chrome of its own. `form` and `errors` come from ../listeners; `support` is
 * what the sidecar reported it accepts (../schema sidecarSupport).
 */
export default function ListenerForm({ form, setField, errors, support = ALL_SUPPORTED }) {
  const ctx = { form, setField, errors, support }
  const visible = LISTENER_FIELDS.filter((f) => appliesTo(f, form.protocol))
  const basic = visible.filter((f) => f.basic)
  // A required block (ssh) holds required fields, which must not hide in Advanced.
  const required = visible.filter((f) => !f.basic && f.type === 'object' && f.required)
  const advanced = visible.filter((f) => !f.basic && !required.includes(f))
  const scalars = advanced.filter((f) => f.type !== 'object')
  const blocks = advanced.filter((f) => f.type === 'object')

  return (
    <Stack gap="xxlAlt">
      <SectionRow
        title="Listener"
        description="Where clients reach the sidecar, where the sidecar reaches your resource, and the protocol between them."
      >
        <Fields fields={basic} prefix="" {...ctx} />
      </SectionRow>

      {required.map((f) => (
        <SectionRow key={f.key} title={f.label} description={f.help}>
          <Fields fields={f.fields ?? []} prefix={f.key} {...ctx} />
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
