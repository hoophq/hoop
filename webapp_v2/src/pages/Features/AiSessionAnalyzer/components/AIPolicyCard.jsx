import { useId, useState } from 'react'
import { Group, Paper, ScrollArea, Stack, Text } from '@mantine/core'
import { PackagePlus, Search } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Checkbox from '@/components/Checkbox'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import Switch from '@/components/Switch'
import TextInput from '@/components/TextInput'
import { useUserStore } from '@/stores/useUserStore'
import { AI_RULE_FEATURES } from '@/pages/sidecarRuleVocabulary'
import SettingsCard from './SettingsCard'

const EMPTY_LIBRARY = {
  guardrails: 'No guardrail rule runs on sidecars yet.',
  mask: 'No data masking rule runs on sidecars yet.',
}

// The library rules of one feature the AI may apply. `rules` null selects
// every one (designer comment #94: all selected by default).
function RuleList({ featureKey, options, rules, create, status, onToggle, onSelectAll, disabled }) {
  const [query, setQuery] = useState('')
  const selected = new Set(rules ?? options.map((r) => r.name))
  const count = options.filter((r) => selected.has(r.name)).length
  const needle = query.trim().toLowerCase()
  const shown = options.filter(
    (r) => r.name.toLowerCase().includes(needle) || r.description.toLowerCase().includes(needle),
  )

  let empty = null
  if (status === 'loading' || status === 'idle') empty = 'Loading rules…'
  else if (status === 'error' && options.length === 0) empty = 'The rules failed to load.'
  else if (options.length === 0) empty = EMPTY_LIBRARY[featureKey]
  else if (shown.length === 0) empty = 'No rule matches the search.'

  return (
    <Paper withBorder radius="md" p="md">
      <Stack gap="sm">
        <Text size="sm" fw={500}>
          Rules the AI can use
        </Text>
        <TextInput
          size="sm"
          placeholder="Search rules"
          aria-label="Search rules"
          leftSection={<Search size={14} aria-hidden="true" />}
          value={query}
          onChange={(e) => setQuery(e.currentTarget.value)}
        />
        <Group justify="space-between" wrap="nowrap">
          <Text size="xs" c="dimmed">
            {`${count} of ${options.length} selected`}
          </Text>
          {/* Hidden when nothing can be selected; clicking it with all selected changes nothing. */}
          {!disabled && options.length > 0 && (
            <Button variant="transparent" size="xs" px={0} onClick={onSelectAll}>
              Select all
            </Button>
          )}
        </Group>
        <ScrollArea.Autosize mah={170} type="auto" offsetScrollbars>
          <Stack gap="sm">
            {shown.map((rule) => (
              <Checkbox
                key={rule.name}
                label={
                  <Text size="sm" fw={600}>
                    {rule.name}
                  </Text>
                }
                description={rule.description || undefined}
                checked={selected.has(rule.name)}
                disabled={disabled}
                onChange={() => onToggle(rule.name)}
              />
            ))}
            {empty && (
              <Text size="sm" c="dimmed">
                {empty}
              </Text>
            )}
          </Stack>
        </ScrollArea.Autosize>
        {count === 0 && options.length > 0 && (
          <Text size="xs" c="dimmed">
            {create
              ? 'With no rule selected, the AI can only create one-time rules.'
              : 'With no rule selected, the AI applies no rule of this kind.'}
          </Text>
        )}
      </Stack>
    </Paper>
  )
}

// One feature row: a toggle and, when on, its rules and the one-time rule box.
// A feature the license does not grant cannot be turned on; one already on
// can still be turned off.
function FeatureRow({ feature, value, options, status, locked, onChange, onToggleRule }) {
  const { key, label, description } = feature
  const switchId = useId()
  const descriptionId = `${switchId}-description`
  return (
    <Paper withBorder radius="md" p="md">
      <Stack gap="md">
        <Group justify="space-between" wrap="nowrap" align="flex-start">
          <Stack gap={4}>
            <Text size="sm" fw={600} component="label" htmlFor={switchId}>
              {label}
            </Text>
            <Text size="xs" c="dimmed" id={descriptionId}>
              {description}
            </Text>
          </Stack>
          <Switch
            id={switchId}
            aria-describedby={descriptionId}
            checked={value.on}
            disabled={locked && !value.on}
            onChange={(e) => onChange({ on: e.currentTarget.checked })}
          />
        </Group>
        {value.on && (
          <>
            <RuleList
              featureKey={key}
              options={options}
              rules={value.rules}
              create={value.create}
              status={status}
              disabled={locked}
              onToggle={onToggleRule}
              onSelectAll={() => onChange({ rules: null })}
            />
            <Paper radius="md" p="md" bg="gray.1">
              <Checkbox
                label={
                  <Text size="sm" fw={600}>
                    Let the AI create one-time rules
                  </Text>
                }
                description="A new rule applies to this statement only. It is not added to your rules list."
                checked={value.create}
                disabled={locked}
                onChange={(e) => onChange({ create: e.currentTarget.checked })}
              />
            </Paper>
          </>
        )}
      </Stack>
    </Paper>
  )
}

// The "AI policy settings" card of one risk level (ENG-662): which guardrail
// and data masking rules the AI may apply to a statement of this level.
// `helper` says when the rules apply. `unavailable` replaces the rows with a
// reason when the level cannot carry AI rules.
export default function AIPolicyCard({ ai, status, optionsFor, onFeature, onToggleRule, helper, unavailable }) {
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  // Subscribed so a license change re-renders; the check itself is the store's.
  useUserStore((s) => s.licenseFeatures)
  const isLicenseFeatureEnabled = useUserStore((s) => s.isLicenseFeatureEnabled)
  const configured = !unavailable && AI_RULE_FEATURES.some(({ key }) => ai[key].on)

  return (
    <SettingsCard
      icon={PackagePlus}
      title="AI policy settings"
      subtitle="Let the AI choose which rules protect each statement in this risk level"
      panel={false}
      badge={
        isFreeLicense ? (
          <Badge variant="light" color="indigo" tag>
            Enterprise
          </Badge>
        ) : null
      }
      done={configured}
    >
      {unavailable ? (
        <Text size="sm" c="dimmed">
          {unavailable}
        </Text>
      ) : (
        <Stack gap="sm">
          {isFreeLicense && <FreeLicenseCallout message="Letting the AI apply rules is an Enterprise feature." />}
          {AI_RULE_FEATURES.map((feature) => (
            <FeatureRow
              key={feature.key}
              feature={feature}
              value={ai[feature.key]}
              options={optionsFor(feature.key)}
              status={status}
              locked={isFreeLicense || !isLicenseFeatureEnabled(feature.license)}
              onChange={(patch) => onFeature(feature.key, patch)}
              onToggleRule={(name) => onToggleRule(feature.key, name)}
            />
          ))}
          {helper && (
            <Text size="xs" c="dimmed">
              {helper}
            </Text>
          )}
        </Stack>
      )}
    </SettingsCard>
  )
}
