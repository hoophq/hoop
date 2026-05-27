import { Stack } from '@mantine/core';
import { ArrowUpRight, Star, ExternalLink } from 'lucide-react';
import Button from '@/components/Button';
import Alert from '@/components/Alert';
import MultiSelect from '@/components/MultiSelect';
import { useUserStore } from '@/stores/useUserStore';
import { useConfigureRoleStore } from '../store';
import { dlpOptionsFor } from '../utils/dlpInfoTypes';
import ToggleSection from '../components/ToggleSection';

// Shared AI Data Masking section used by both Terminal Access and
// Native Access tabs. Gated by license and gateway capabilities, and
// the type-list is driven by the gateway's configured redact_provider
// (Presidio by default; GCP if explicitly configured).
export default function AIDataMaskingSection() {
  const drafts = useConfigureRoleStore(s => s.drafts);
  const setDraft = useConfigureRoleStore(s => s.setDraft);
  const { isFreeLicense, hasRedactCredentials, redactProvider } = useUserStore();

  const dlpOptions = dlpOptionsFor(redactProvider).map(t => ({ value: t, label: t }));
  const disabled = isFreeLicense || !hasRedactCredentials || redactProvider === 'mspresidio';

  return (
    <ToggleSection
      title="AI Data Masking"
      description="Provide an additional layer of security by ensuring sensitive data is masked in query results with AI-powered data masking."
      checked={drafts.redact_enabled}
      disabled={disabled}
      onChange={checked => setDraft({ redact_enabled: checked })}
      complement={
        drafts.redact_enabled &&
        <MultiSelect
          placeholder="Select info types"
          searchable
          data={dlpOptions}
          value={drafts.redact_types}
          onChange={value => setDraft({ redact_types: value })}
          disabled={disabled}
        />
      }
      learnMore={
        <Stack gap="xs" align="flex-start">
          {isFreeLicense &&
            <Alert variant="light" color="indigo" icon={<Star size={16} />} w="100%">
              {'Enable AI Data Masking by upgrading your plan.'}
            </Alert>}
          <Button
            variant="default"
            size="xs"
            leftSection={<ArrowUpRight size={14} />}
            component="a"
            href="https://hoop.dev/docs/features/ai-data-masking"
            target="_blank"
            rel="noopener noreferrer"
          >
            Learn more about AI Data Masking
          </Button>
          {redactProvider === 'mspresidio' &&
            <Button
              variant="default"
              size="xs"
              leftSection={<ExternalLink size={14} />}
              component="a"
              href="/ai-data-masking"
            >
              Go to AI Data Masking Management
            </Button>}
        </Stack>
      }
    />
  );
}
