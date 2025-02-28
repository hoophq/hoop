import React from 'react';
import { Avatar, Box, Flex, Heading, Text } from '@radix-ui/themes';
import { LucideIcon } from 'lucide-react';

interface FeatureProps {
  icon: LucideIcon;
  title: string;
  description: string;
}

export const Feature: React.FC<FeatureProps> = ({ icon: Icon, title, description }) => {
  return (
    <Flex align="center" gap="4">
      <Avatar
        fallback={<Icon size={20} />}
        size="4"
      />
      <Box>
        <Heading as="h3" size="5" weight="bold" className="text-[--gray-12]">
          {title}
        </Heading>
        <Text size="3" className="text-[--gray-12]">
          {description}
        </Text>
      </Box>
    </Flex>
  );
}; 
