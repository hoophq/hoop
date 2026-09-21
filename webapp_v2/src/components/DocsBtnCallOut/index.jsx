import { Box, Flex, Text, Anchor } from '@mantine/core';
import { ArrowUpRight } from 'lucide-react';

// Two looks. The default is the neutral outline every existing caller already
// renders, and it stays byte-identical so no page changes by accident.
//
// `indigo` is the accented one: a link out to the reference, in the primary
// color, for a form that leans on the docs instead of explaining every field
// inline. Radix Indigo 0/2/8 — the lightest step for the fill, the second for
// a border that reads at a glance, and a text step that clears AA on it.
const LOOKS = {
  default: {
    bg: undefined,
    bd: '1px solid var(--mantine-color-default-border)',
    c: 'gray.8',
    icon: 'var(--mantine-color-dimmed)',
    fw: undefined,
  },
  indigo: {
    bg: 'var(--mantine-color-indigo-0)',
    bd: '1px solid var(--mantine-color-indigo-2)',
    c: 'indigo.8',
    icon: 'var(--mantine-color-indigo-6)',
    fw: 500,
  },
};

function DocsBtnCallOut({ text, href, variant = 'default' }) {
  const look = LOOKS[variant] ?? LOOKS.default;
  return (
    <Box bg={look.bg} bd={look.bd} p="sm" bdrs="sm" w="fit-content">
      <Anchor href={href} target="_blank" rel="noopener noreferrer" underline="never">
        <Flex gap="xs" align="center">
          <ArrowUpRight size={16} color={look.icon} />
          <Text size="sm" c={look.c} fw={look.fw}>
            {text}
          </Text>
        </Flex>
      </Anchor>
    </Box>
  );
}

export default DocsBtnCallOut;
