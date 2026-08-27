import { useState } from 'react'
import { Box, Group, SimpleGrid, Stack } from '@mantine/core'
import { PencilRuler } from 'lucide-react'
import Button from '@/components/Button'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import PasswordInput from '@/components/PasswordInput'
import SelectionCard from '@/components/SelectionCard'
import TextInput from '@/components/TextInput'
import { showSnackbar } from '@/utils/snackbar'
import { useAiSessionAnalyzerStore } from './store'
import SectionRow from './components/SectionRow'

// The logos are served from public/images. In webapp_v2 they came from the
// ClojureScript resources tree through a dev proxy; this app has no such proxy, so
// adding a provider means adding its logo to public/ in the same change.
const PROVIDERS = [
  {
    id: 'azure-openai',
    label: 'Azure OpenAI',
    image: '/images/azure-logo.svg',
    docsUrl: 'https://ai.azure.com/catalog/models',
    requiresApiUrl: true,
  },
  {
    id: 'anthropic',
    label: 'Anthropic',
    image: '/images/anthropic-logo.svg',
    docsUrl: 'https://platform.claude.com/docs/en/about-claude/models/overview',
  },
  {
    id: 'openai',
    label: 'OpenAI',
    image: '/images/openai-logo.svg',
    docsUrl: 'https://developers.openai.com/api/docs/models',
  },
  {
    id: 'custom',
    label: 'Custom',
    icon: PencilRuler,
    requiresApiUrl: true,
  },
]

const DEFAULT_PROVIDER = 'openai'

const emptyDraft = () => ({ model: '', apiKey: '', apiUrl: '' })

export default function ConfigureTab({ onSaved }) {
  const provider = useAiSessionAnalyzerStore((s) => s.provider)
  const submitting = useAiSessionAnalyzerStore((s) => s.submitting)
  const saveProvider = useAiSessionAnalyzerStore((s) => s.saveProvider)

  // The page only renders its tabs once the provider fetch has settled, so the
  // saved configuration is available to seed the form on mount.
  const [providerId, setProviderId] = useState(() => provider?.provider ?? DEFAULT_PROVIDER)
  const [draft, setDraft] = useState(() => ({
    model: provider?.model ?? '',
    apiKey: provider?.api_key ?? '',
    apiUrl: provider?.api_url ?? '',
  }))
  // Switching providers stashes what was typed for the outgoing one, so an
  // admin comparing two providers does not lose the credentials of either.
  const [drafts, setDrafts] = useState(() =>
    provider?.provider
      ? {
          [provider.provider]: {
            model: provider.model ?? '',
            apiKey: provider.api_key ?? '',
            apiUrl: provider.api_url ?? '',
          },
        }
      : {},
  )

  const selected = PROVIDERS.find((p) => p.id === providerId) ?? PROVIDERS[0]
  const setField = (patch) => setDraft((d) => ({ ...d, ...patch }))

  const handleProviderChange = (nextId) => {
    if (nextId === providerId) return
    setDrafts((cache) => ({ ...cache, [providerId]: draft }))
    setDraft(drafts[nextId] ?? emptyDraft())
    setProviderId(nextId)
  }

  const handleSave = async () => {
    if (!draft.model.trim()) {
      showSnackbar({ level: 'error', text: 'Model is required.' })
      return
    }
    if (!draft.apiKey.trim()) {
      showSnackbar({ level: 'error', text: 'API Key is required.' })
      return
    }
    if (selected.requiresApiUrl && !draft.apiUrl.trim()) {
      showSnackbar({ level: 'error', text: 'API URL is required for this provider.' })
      return
    }

    const { ok, error } = await saveProvider({
      provider: providerId,
      model: draft.model,
      api_key: draft.apiKey,
      // Providers with a fixed endpoint carry no URL, but the field is always
      // sent so switching from a custom provider clears the previous value.
      api_url: selected.requiresApiUrl ? draft.apiUrl : '',
    })

    if (ok) {
      showSnackbar({ level: 'success', text: 'Configuration saved.' })
      onSaved?.()
    } else {
      showSnackbar({
        level: 'error',
        text: 'Failed to save provider configuration',
        description: error?.response?.data?.message || error?.message,
      })
    }
  }

  return (
    <Stack gap="xxlAlt" pb="xl">
      <SectionRow
        title="Select your provider"
        description="Select between a market or custom model. Custom models need to follow the OpenAI API pattern."
      >
        <Box maw={600}>
          <SimpleGrid cols={{ base: 1, xs: 2, md: 3 }} spacing="sm">
            {PROVIDERS.map((p) => (
              <SelectionCard
                key={p.id}
                icon={p.icon}
                image={p.image}
                title={p.label}
                selected={providerId === p.id}
                onClick={() => handleProviderChange(p.id)}
              />
            ))}
          </SimpleGrid>
        </Box>
      </SectionRow>

      <SectionRow
        title={`${selected.label} configuration`}
        description="Set your provider model and API key to enable AI features."
        callout={
          selected.docsUrl && (
            <DocsBtnCallOut text="See supported AI models" href={selected.docsUrl} />
          )
        }
      >
        <Stack gap="md" maw={600}>
          {selected.requiresApiUrl && (
            <TextInput
              label="API URL"
              placeholder="https://your-endpoint.openai.azure.com/"
              value={draft.apiUrl}
              onChange={(e) => setField({ apiUrl: e.currentTarget.value })}
            />
          )}
          <TextInput
            label="Model"
            placeholder="Enter model name (e.g. gpt-4o)"
            value={draft.model}
            onChange={(e) => setField({ model: e.currentTarget.value })}
          />
          <PasswordInput
            label="API Key"
            placeholder="Insert your API Key"
            value={draft.apiKey}
            onChange={(e) => setField({ apiKey: e.currentTarget.value })}
          />
          <Group justify="flex-start">
            <Button onClick={handleSave} loading={submitting} disabled={submitting}>
              Save
            </Button>
          </Group>
        </Stack>
      </SectionRow>
    </Stack>
  )
}
