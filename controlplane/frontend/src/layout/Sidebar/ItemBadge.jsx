import { Badge, Group, Text } from '@mantine/core';
import classes from './Sidebar.module.css';

export function ItemBadge({ badge, shortcut }) {
  const hasBadge = !!badge;
  const hasShortcut = !!shortcut;
  if (!hasBadge && !hasShortcut) return null;

  return (
    <Group gap={4} wrap="nowrap">
      {hasShortcut && (
        <Text className={classes.shortcutText}>{shortcut}</Text>
      )}
      {hasBadge && (
        <Badge size="xs" variant="filled" color={badge.color} className={classes.badgeFilled}>
          {badge.text}
        </Badge>
      )}
    </Group>
  );
}
