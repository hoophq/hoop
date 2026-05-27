import { Stack, Title, Text } from '@mantine/core'
import { Link } from 'react-router-dom'
import { ArrowUpRight } from 'lucide-react'
import Button from '@/components/Button'
import MultiSelect from '@/components/MultiSelect'
import Select from '@/components/Select'
import { useConfigureRoleStore } from '../store'
import ToggleSection from './ToggleSection'
import AIDataMaskingSection from './AIDataMaskingSection'
import MetadataFieldsInput from './MetadataFieldsInput'
import ReviewSection from './ReviewSection'
import { hasReviewConfig } from '../utils/reviewConfig'

// Terminal Access tab: every per-connection setting that affects how
// commands run from the web terminal or hoop CLI's exec/connect flows.
// The CLJS version also exposes a backward-compat "Review by Command"
// section that's only shown when the connection already has a review
// config — same omission as NativeAccessTab, falls back to legacy
// editor for review-configured connections.
export default function TerminalAccessTab({ connection }) {
  const drafts = useConfigureRoleStore((s) => s.drafts)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)
  const guardrailsList = useConfigureRoleStore((s) => s.guardrailsList)
  const jiraTemplatesList = useConfigureRoleStore((s) => s.jiraTemplatesList)

  const guardrailOptions = guardrailsList.map((g) => ({
    value: g.id,
    label: g.name,
  }))
  const jiraOptions = jiraTemplatesList.map((t) => ({
    value: t.id,
    label: t.name,
  }))

  const isDatabase = connection.type === 'database'
  const showReview = hasReviewConfig(connection)

  return (
    <Stack gap="xl" maw={720}>
      <ToggleSection
        title="Terminal access availability"
        description="Use hoop.dev's Web Terminal or our CLI's One-Offs commands directly in your terminal."
        checked={drafts.access_mode_exec === 'enabled'}
        onChange={(checked) =>
          setDraft({ access_mode_exec: checked ? 'enabled' : 'disabled' })
        }
      />

      {showReview && <ReviewSection kind="command" />}

      <AIDataMaskingSection />

      <ToggleSection
        title="Runbooks"
        description="Automate tasks in your organization from a git server source."
        checked={drafts.access_mode_runbooks === 'enabled'}
        onChange={(checked) =>
          setDraft({ access_mode_runbooks: checked ? 'enabled' : 'disabled' })
        }
        learnMore={
          <Button
            variant="default"
            size="xs"
            leftSection={<ArrowUpRight size={14} />}
            w="fit-content"
            component="a"
            href="https://hoop.dev/docs/features/runbooks"
            target="_blank"
            rel="noopener noreferrer"
          >
            Learn more about Runbooks
          </Button>
        }
      />

      <Stack gap="md">
        <Stack gap="xs">
          <Title order={4}>Guardrails</Title>
          <Text size="sm" c="dimmed">
            Create custom rules to guide and protect usage within your
            resource roles.
          </Text>
        </Stack>
        <MultiSelect
          placeholder="Select..."
          searchable
          nothingFoundMessage="No guardrails created yet. Go to Guardrails to add one."
          data={guardrailOptions}
          value={drafts.guardrail_rules}
          onChange={(value) => setDraft({ guardrail_rules: value })}
        />
        <Button
          variant="default"
          size="xs"
          leftSection={<ArrowUpRight size={14} />}
          w="fit-content"
          component={Link}
          to="/guardrails"
        >
          Go to Guardrails
        </Button>
      </Stack>

      <Stack gap="md">
        <Stack gap="xs">
          <Title order={4}>Jira Templates</Title>
          <Text size="sm" c="dimmed">
            Optimize and automate workflows with Jira Integration.
          </Text>
        </Stack>
        <Select
          placeholder="Select..."
          clearable
          nothingFoundMessage="No Jira templates configured. Go to JIRA Integration to add one."
          data={jiraOptions}
          value={drafts.jira_issue_template_id || null}
          onChange={(value) => setDraft({ jira_issue_template_id: value || '' })}
        />
        <Button
          variant="default"
          size="xs"
          leftSection={<ArrowUpRight size={14} />}
          w="fit-content"
          component={Link}
          to="/jira-templates"
        >
          Go to JIRA Integration
        </Button>
      </Stack>

      <Stack gap="md">
        <Stack gap="xs">
          <Title order={4}>Require specific metadata</Title>
          <Text size="sm" c="dimmed">
            Include mandatory metadata to be filled before executing
            commands on this resource role.
          </Text>
        </Stack>
        <MetadataFieldsInput />
      </Stack>

      {isDatabase && (
        <Stack gap="md">
          <Title order={4} mt="md">Additional configuration</Title>
          <ToggleSection
            title="Database schema"
            description="Show database schema in the Editor section."
            checked={drafts.access_schema === 'enabled'}
            onChange={(checked) =>
              setDraft({ access_schema: checked ? 'enabled' : 'disabled' })
            }
          />
        </Stack>
      )}
    </Stack>
  )
}
