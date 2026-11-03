import { Badge, Group, Text } from '@mantine/core';
import classes from './Sidebar.module.css';

export function ItemBadge({ badge, count, shortcut }) {
  const hasBadge = !!badge;
  const hasCount = count > 0;
  const hasShortcut = !!shortcut;
  if (!hasBadge && !hasCount && !hasShortcut) return null;

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
      {hasCount && (
        <Badge
          size="md"
          circle={count < 100}
          variant="filled"
          color="var(--mantine-color-text)"
          className={classes.badgeFilled}
        >
          {count > 99 ? '99+' : String(count)}
        </Badge>
      )}
    </Group>
  );
}
