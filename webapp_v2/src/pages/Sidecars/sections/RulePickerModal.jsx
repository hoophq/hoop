import { useEffect, useMemo, useState } from 'react'
import { Group, ScrollArea, Stack, Text, Title } from '@mantine/core'
import { Search } from 'lucide-react'
import Button from '@/components/Button'
import Checkbox from '@/components/Checkbox'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import TextInput from '@/components/TextInput'
import { showSnackbar } from '@/utils/snackbar'
import { RULE_APIS, applyBindings, boundCount, ruleErrorMessage, targetLanes } from '../rules'

const matches = (rule, needle) =>
  [rule.name, rule.description].some((v) => String(v ?? '').toLowerCase().includes(needle))

// Mounted for one opening of the dialog, so every state here starts fresh.
function Picker({ feature, sidecar, listener, onClose, onCreate, onSaved }) {
  const api = RULE_APIS[feature.key]
  // A listener runs one analyzer block, so that picker takes one rule.
  const single = feature.key === 'ai-analyzer'
  const lanes = useMemo(() => targetLanes(sidecar, listener), [sidecar, listener])
  const [rules, setRules] = useState([])
  const [status, setStatus] = useState('loading')
  const [query, setQuery] = useState('')
  // The user's choices, kept apart from what the bindings say: a refresh of
  // the sidecar mid-dialog moves the baseline, not the choices.
  const [choices, setChoices] = useState(() => new Map())
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let cancelled = false
    api
      .list()
      .then((list) => {
        if (cancelled) return
        setRules(list)
        setStatus('ready')
      })
      .catch(() => {
        if (!cancelled) setStatus('error')
      })
    return () => {
      cancelled = true
    }
  }, [api])

  // Checked when the rule reaches every lane in scope; on some of them only,
  // it shows indeterminate until toggled either way.
  const bound = useMemo(() => {
    const all = new Set()
    const some = new Set()
    for (const rule of rules) {
      const n = boundCount(sidecar.bound_rules, api.kind, rule.name, lanes)
      if (n > 0 && n === lanes.length) all.add(rule.name)
      else if (n > 0) some.add(rule.name)
    }
    return { all, some }
  }, [rules, sidecar.bound_rules, api.kind, lanes])

  const isOn = (name) => (choices.has(name) ? choices.get(name) : bound.all.has(name))
  const enabled = rules.filter((r) => !r.managed_by)

  const choose = (names, on) =>
    setChoices((current) => {
      const next = new Map(current)
      for (const name of names) {
        // Back on the baseline is no choice, unless the rule was on some
        // lanes only: "off" then means unbinding those too.
        if (on === bound.all.has(name) && !bound.some.has(name)) next.delete(name)
        else next.set(name, on)
      }
      return next
    })

  const toggle = (name) => {
    const on = !isOn(name)
    if (single && on) {
      choose(
        enabled.filter((r) => r.name !== name && isOn(r.name)).map((r) => r.name),
        false,
      )
    }
    choose([name], on)
  }

  const allOn = enabled.length > 0 && enabled.every((r) => isOn(r.name))
  const someOn = enabled.some((r) => isOn(r.name))
  const count = rules.filter((r) => isOn(r.name)).length
  const changes = enabled.filter((r) => choices.has(r.name)).map((r) => ({ rule: r, on: choices.get(r.name) }))
  const needle = query.trim().toLowerCase()
  const visible = needle ? rules.filter((r) => matches(r, needle)) : rules
  const scope = listener ? listener.name : sidecar.name

  const save = async () => {
    setSaving(true)
    const failed = await applyBindings(api, sidecar, lanes, changes)
    setSaving(false)
    onSaved()
    if (failed.length === 0) {
      showSnackbar({ level: 'success', text: `${feature.label} rules updated for ${scope}.` })
      onClose()
      return
    }
    // The rest went through; only the refused choices stay in the dialog.
    const names = new Set(failed.map((f) => f.rule.name))
    setChoices((current) => new Map([...current].filter(([name]) => names.has(name))))
    showSnackbar({
      level: 'error',
      text: failed.length === changes.length ? 'The rules could not be saved.' : 'Some rules could not be saved.',
      description: failed.map((f) => `${f.rule.name}: ${ruleErrorMessage(f.error)}`).join(' '),
    })
  }

  return (
    <Stack gap="lgAlt" py="lgAlt">
      <Stack gap="md" px="lgAlt">
        <Stack gap="xs">
          <Title order={2}>{feature.label}</Title>
          <Text size="sm" c="dimmed">
            {`Choose which rules apply to ${scope}`}
          </Text>
        </Stack>
        <Group gap="md" wrap="nowrap">
          <TextInput
            flex={1}
            placeholder="Search"
            aria-label="Search rules"
            leftSection={<Search size={16} />}
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
          />
          <Button variant="light" onClick={onCreate} flex="0 0 auto">
            Create rule
          </Button>
        </Group>
      </Stack>

      {status === 'loading' && <PageLoader h={160} />}
      {status === 'error' && (
        <Text size="sm" c="red" px="lgAlt">
          {`Failed to load the ${feature.label} rules.`}
        </Text>
      )}
      {status === 'ready' && rules.length === 0 && (
        <Text size="sm" c="dimmed" px="lgAlt">
          {`No ${feature.label} rules yet. Create one to apply it here.`}
        </Text>
      )}
      {status === 'ready' && rules.length > 0 && (
        <Stack gap="md">
          {!single && (
            <Group px="lgAlt" py="xs" bg="gray.0">
              <Checkbox
                label={
                  <Text size="sm" fw={500} c="dimmed">
                    Select all
                  </Text>
                }
                checked={allOn}
                indeterminate={someOn && !allOn}
                disabled={enabled.length === 0}
                onChange={() => choose(enabled.map((r) => r.name), !allOn)}
              />
            </Group>
          )}
          <ScrollArea.Autosize mah={380} type="auto">
            <Stack gap="lgAlt" px="lgAlt" py={4}>
              {visible.map((rule) => (
                <Checkbox
                  key={rule.name}
                  checked={isOn(rule.name)}
                  indeterminate={!choices.has(rule.name) && bound.some.has(rule.name)}
                  disabled={Boolean(rule.managed_by)}
                  onChange={() => toggle(rule.name)}
                  label={
                    <Text size="sm" fw={700}>
                      {rule.name}
                    </Text>
                  }
                  description={
                    rule.managed_by ? 'Managed by Hoop and cannot be applied here.' : rule.description || undefined
                  }
                />
              ))}
              {visible.length === 0 && (
                <Text size="sm" c="dimmed">
                  {`No rule matches "${query.trim()}".`}
                </Text>
              )}
            </Stack>
          </ScrollArea.Autosize>
        </Stack>
      )}

      <Group justify="space-between" px="lgAlt">
        <Text size="sm">{`${count} of ${rules.length} selected`}</Text>
        <Group gap="sm">
          <Button variant="default" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={save} loading={saving} disabled={changes.length === 0}>
            Save
          </Button>
        </Group>
      </Group>
    </Stack>
  )
}

/**
 * "Choose which rules apply": every rule of one feature the organization
 * has, with the ones already reaching this sidecar or listener checked.
 * Saving rewrites each rule whose box changed, one PUT each, with the
 * listener set it ends up bound to. A sidecar-level pick reaches every named
 * listener.
 */
export default function RulePickerModal({ opened, feature, sidecar, listener, onClose, onCreate, onSaved }) {
  if (!feature) return null
  return (
    <Modal opened={opened} onClose={onClose} size={892} withCloseButton={false} padding={0}>
      <Picker
        feature={feature}
        sidecar={sidecar}
        listener={listener}
        onClose={onClose}
        onCreate={onCreate}
        onSaved={onSaved}
      />
    </Modal>
  )
}
