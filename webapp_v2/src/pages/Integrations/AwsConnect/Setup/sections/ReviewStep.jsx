import { Anchor, Group, Stack, Text, Title } from '@mantine/core'
import { ArrowUpRight } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Select from '@/components/Select'
import Switch from '@/components/Switch'
import TextInput from '@/components/TextInput'
import { useAgentStore } from '@/stores/useAgentStore'
import { docsUrl } from '@/utils/docsUrl'
import TreeTable from '../../components/TreeTable'
import { useAwsConnectSetupStore } from '../store'
import { roleName, selectedTree } from '../wizard'

const INPUT_MIN_WIDTH = 220

function DocsLink({ href, children }) {
  return (
    <Anchor href={href} target="_blank" rel="noopener noreferrer" size="sm">
      <Group gap={4} component="span">
        {children}
        <ArrowUpRight size={16} aria-hidden />
      </Group>
    </Anchor>
  )
}

function AdditionalConfiguration() {
  const createConnection = useAwsConnectSetupStore((s) => s.createConnection)
  const vaultEnabled = useAwsConnectSetupStore((s) => s.vaultEnabled)
  const vaultPath = useAwsConnectSetupStore((s) => s.vaultPath)
  const update = useAwsConnectSetupStore((s) => s.update)

  return (
    <Stack gap="lg" maw={600} w="100%">
      <Title order={3}>Additional Configuration</Title>
      <Switch
        checked={createConnection}
        onChange={(e) => update({ createConnection: e.currentTarget.checked })}
        label="Create Resource Role"
        description="When enabled, resource roles will be automatically created after configuring the resources."
      />
      <Stack gap="sm">
        <Switch
          checked={vaultEnabled}
          onChange={(e) => update({ vaultEnabled: e.currentTarget.checked })}
          label="Enable Vault Secrets Provider"
          description="Integrate with HashiCorp Vault to dynamically expand environment variables in your resource roles. Currently, only Vault is supported."
        />
        <DocsLink href={docsUrl.setup.configuration.secretsManager}>
          Learn more about Secrets Manager Configuration
        </DocsLink>
        {vaultEnabled && (
          <TextInput
            label="Vault Path"
            placeholder="e.g. dbsecrets/data/"
            value={vaultPath}
            onChange={(e) => update({ vaultPath: e.currentTarget.value })}
          />
        )}
      </Stack>
    </Stack>
  )
}

export default function ReviewStep({ validationError, onAgentChange }) {
  const agents = useAgentStore((s) => s.agents)
  const tree = useAwsConnectSetupStore((s) => s.tree)
  const selected = useAwsConnectSetupStore((s) => s.selectedResources)
  const roleNames = useAwsConnectSetupStore((s) => s.roleNames)
  const assignments = useAwsConnectSetupStore((s) => s.agentAssignments)
  const setMapValue = useAwsConnectSetupStore((s) => s.setMapValue)

  const rows = selectedTree(tree, selected)
  const agentOptions = agents.map((agent) => ({ value: agent.id, label: agent.name }))

  const assignAgent = (resourceId, agentId) => {
    setMapValue('agentAssignments', resourceId, agentId)
    onAgentChange()
  }
  const applyToAccount = (accountId, agentId) => {
    const account = rows.find((row) => row.id === accountId)
    for (const child of account?.children ?? []) assignAgent(child.id, agentId)
  }

  const columns = [
    { id: 'name', header: 'Resources' },
    { id: 'account', header: 'Account ID', render: (row) => (row.isAccount ? row.id : '') },
    {
      id: 'roleName',
      header: 'Resource Role Name',
      render: (row) =>
        row.isAccount ? (
          ''
        ) : (
          <TextInput
            miw={INPUT_MIN_WIDTH}
            aria-label={`Resource role name for ${row.name}`}
            placeholder="Enter resource role name"
            value={roleName(row, roleNames)}
            onChange={(e) => setMapValue('roleNames', row.id, e.currentTarget.value)}
          />
        ),
    },
    {
      id: 'agent',
      header: 'Agent',
      render: (row) => {
        if (row.isAccount) return ''
        const agentId = assignments[row.id] ?? null
        return (
          <Group gap="xs" wrap="nowrap">
            <Select
              flex={1}
              miw={INPUT_MIN_WIDTH}
              aria-label={`Agent for ${row.name}`}
              placeholder="Select an agent"
              data={agentOptions}
              value={agentId}
              onChange={(value) => assignAgent(row.id, value)}
            />
            <Button
              size="sm"
              variant="light"
              flex="none"
              disabled={!agentId}
              onClick={() => applyToAccount(row.accountId, agentId)}
            >
              Apply to all
            </Button>
          </Group>
        )
      },
    },
  ]

  return (
    <Stack gap="xl" w="100%" align="center">
      <Stack gap="xs" maw={600} w="100%" align="flex-start">
        <Title order={3}>Review Selected Resources</Title>
        <Text size="sm" c="dimmed">
          Please review your selected AWS database resources and assign an Agent to each resource role before
          proceeding. You can also customize the resource role names.
        </Text>
        <DocsLink href={docsUrl.concepts.agents}>Learn more about Agents</DocsLink>
      </Stack>

      <AdditionalConfiguration />

      {validationError && (
        <Alert color="red" variant="light" role="alert" maw={600} w="100%">
          {validationError}
        </Alert>
      )}

      <TreeTable
        columns={columns}
        rows={rows}
        empty="No resources selected yet"
        defaultExpanded={rows.map((row) => row.id)}
      />
    </Stack>
  )
}
