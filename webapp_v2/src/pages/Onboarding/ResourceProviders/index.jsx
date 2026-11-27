import { useNavigate } from 'react-router-dom'
import { Center, Stack, Text, Title } from '@mantine/core'
import ActionCard from '@/components/ActionCard'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import FormFooter, { FORM_FOOTER_CLEARANCE } from '@/components/FormFooter'
import OnboardingLayout, { OnboardingLogo } from '@/layout/OnboardingLayout'
import { SETUP_PATH } from '../onboarding'

// /onboarding/resource-providers: where automatic discovery looks for Resources.
export default function ResourceProviders() {
  const navigate = useNavigate()
  return (
    <OnboardingLayout logout>
      <Center pb={FORM_FOOTER_CLEARANCE}>
        <Stack gap="xl" w="100%" maw={600} align="center">
          <OnboardingLogo />
          <Stack gap="sm" align="center">
            <Title order={1} ta="center">
              What type of Resource are you connecting to?
            </Title>
            <Text size="md" c="dimmed" ta="center">
              Choose the service provider of your Resources.
            </Text>
          </Stack>
          <Stack gap="md" w="100%">
            <ActionCard
              image="/icons/automatic-resources/aws.svg"
              title="Amazon Web Services"
              description="Access AWS to retrieve and connect your database Resources."
              onClick={() => navigate('/onboarding/aws-connect')}
            />
            <ActionCard
              image="/icons/automatic-resources/mongodb.svg"
              title="MongoDB Atlas"
              badge={
                <Badge variant="light" color="gray" tag chip>
                  SOON
                </Badge>
              }
              description="Access your Resources on Atlas Database."
              disabled
            />
          </Stack>
        </Stack>
      </Center>
      <FormFooter
        left={
          <Button variant="light" color="gray" onClick={() => navigate(SETUP_PATH)}>
            Back
          </Button>
        }
      />
    </OnboardingLayout>
  )
}
