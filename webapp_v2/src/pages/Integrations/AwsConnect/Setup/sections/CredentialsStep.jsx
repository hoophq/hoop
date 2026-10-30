import { Stack, Text, Title } from '@mantine/core'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import PasswordInput from '@/components/PasswordInput'
import Radio from '@/components/Radio'
import Select from '@/components/Select'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import { useAwsConnectSetupStore } from '../store'
import { AUTH_CREDENTIALS, AUTH_GATEWAY_PROFILE, AWS_REGIONS } from '../wizard'

export default function CredentialsStep() {
  const authMethod = useAwsConnectSetupStore((s) => s.authMethod)
  const credentials = useAwsConnectSetupStore((s) => s.credentials)
  const error = useAwsConnectSetupStore((s) => s.credentialsError)
  const update = useAwsConnectSetupStore((s) => s.update)
  const setCredential = useAwsConnectSetupStore((s) => s.setCredential)
  const keys = authMethod === AUTH_CREDENTIALS

  return (
    <Stack gap="xl" maw={600} w="100%">
      <Stack gap="xs">
        <Title order={3}>Authentication Method</Title>
        <Text size="sm" c="dimmed">
          Choose how access your AWS resources:
        </Text>
      </Stack>

      <Radio.Group value={authMethod} onChange={(value) => update({ authMethod: value })} aria-label="Authentication Method">
        <Stack gap="md">
          <Radio
            value={AUTH_GATEWAY_PROFILE}
            label="Gateway Instance Profile"
            description="Automatically uses the IAM role attached to the Hoop.dev gateway. No credential entry required - the system handles authentication seamlessly."
          />
          <Radio
            value={AUTH_CREDENTIALS}
            label="AWS Credentials"
            description="Manually provide your AWS access keys and configuration in the fields below. Use this option when specific credentials are needed or when Gateway Instance Profile isn't suitable."
          />
        </Stack>
      </Radio.Group>

      <Stack gap="xs">
        <Title order={3}>{keys ? 'IAM User Credentials' : 'AWS Authentication'}</Title>
        <Text size="sm" c="dimmed">
          {keys
            ? 'These keys provide secure programmatic access to your AWS environment and will be used only for discovering and managing your selected resources.'
            : 'Please provide the information below to proceed with your AWS authentication.'}
        </Text>
      </Stack>

      <Stack gap="md">
        {keys && (
          <>
            <TextInput
              label="Access Key ID"
              placeholder="e.g. AKIAIOSFODNN7EXAMPLE"
              value={credentials.accessKeyId}
              onChange={(e) => setCredential('accessKeyId', e.currentTarget.value)}
            />
            <PasswordInput
              label="Secret Access Key"
              placeholder="e.g. wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
              value={credentials.secretAccessKey}
              onChange={(e) => setCredential('secretAccessKey', e.currentTarget.value)}
            />
            <Textarea
              label="Session Token (Optional)"
              placeholder="e.g. FOoGZXlvYXdzE0z/EaDFQNA2EY59z3tKrAdJB"
              value={credentials.sessionToken}
              onChange={(e) => setCredential('sessionToken', e.currentTarget.value)}
            />
          </>
        )}
        <Select
          label="Region"
          placeholder="Select one"
          data={AWS_REGIONS}
          searchable
          value={credentials.region}
          onChange={(value) => setCredential('region', value)}
        />
      </Stack>

      <Alert color="indigo" variant="light" icon={<Info size={16} />}>
        {
          "Before finishing the setup on the Review and Create step, the root passwords for your selected database resources will be automatically reset for security purposes. This action can't be undone."
        }
      </Alert>

      {error && (
        <Alert color="red" variant="light" role="alert">
          {error}
        </Alert>
      )}
    </Stack>
  )
}
