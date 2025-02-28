import React from 'react';
import { Box, Button, Flex, Heading, Text } from '@radix-ui/themes';
import { ListChecks, MessagesSquare, Sparkles } from 'lucide-react';
import { Feature } from './Feature';
import { HeaderBack } from '../../components/HeaderBack';
import { dispatch } from '../../utils/reframe';
import config from 'goog:webapp.config';

interface UpgradePlanProps {
  removeBack?: boolean;
}

export const UpgradePlan: React.FC<UpgradePlanProps> = ({ removeBack = false }) => {
  const handleRequestDemo = () => {
    dispatch([':modal->close']);
    window.Intercom?.('showNewMessage', 'I want to upgrade my current plan');
  };

  console.log(config);

  return (
    <Box className="bg-white relative overflow-hidden">
      {!removeBack && (
        <Box p="5">
          <HeaderBack />
        </Box>
      )}

      <Flex align="center" justify="between" gap="8" p="9">
        <Box className="w-2/3 xl:w-1/2 space-y-12 pr-0 2xl:pr-16">
          <Box className="space-y-4">
            <Heading as="h1" size="8" weight="bold" className="text-[--gray-12]">
              Get more for your connections
            </Heading>
            <Text size="5" className="text-[--gray-11]">
              Upgrade to Enterprise plan and boost your experience.
            </Text>
          </Box>

          <Box className="space-y-8">
            <Feature
              icon={Sparkles}
              title="AI-Enhanced developer experience"
              description="Power up development with AI-driven query suggestions and automated data masking while maintaining security standards."
            />

            <Feature
              icon={ListChecks}
              title="Complete visibility & control"
              description="Monitor database and infrastructure interaction with detailed session recordings and instant alerts in your favorite tools."
            />

            <Feature
              icon={MessagesSquare}
              title="Enterprise-grade support"
              description="Access priority support through Slack, Teams, or email, plus dedicated onboarding to accelerate your team experience."
            />
          </Box>

          <Button size="4" onClick={handleRequestDemo}>
            Request a demo
          </Button>
        </Box>

        <Box
          className={`
            mt-[--space-9] absolute top-1/2 -translate-y-1/2 right-0 w-1/2 h-auto
            transform translate-x-1/4 xl:translate-x-16 2xl:translate-x-10
          `}
        >
          <Box className="h-full w-full relative">
            <img
              src={`${config.webapp_url}/images/upgrade-plan.png`}
              alt="Terminal interface"
              className="w-full h-[578px] object-cover object-left"
            />
          </Box>
        </Box>
      </Flex>
    </Box>
  );
}; 
