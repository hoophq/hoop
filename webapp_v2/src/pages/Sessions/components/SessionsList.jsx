import { Link, useNavigate } from "react-router-dom";
import { Anchor, Box, Grid, Group, Paper, Stack, Text } from "@mantine/core";
import { Clock } from "lucide-react";
import { formatFullDate } from "@/utils/datetime";
import UserAvatar from "./UserAvatar";
import LiveBadge from "./LiveBadge";
import ReviewStatusBadge from "./ReviewStatusBadge";
import WorkflowChip from "./WorkflowChip";
import { displayNameFor, isLiveSession } from "../utils";
import classes from "./SessionsList.module.css";

/**
 * Port of `session-item` (session_item.cljs:74-125): a bordered list of
 * four-column rows, with NO header — v1 was a Radix `Grid columns="4"`, not a
 * table, and the columns were unlabelled.
 *
 * Accessibility note: v1 rows were bare clickable divs with no tabIndex, role or
 * key handler, so the list was unreachable without a mouse. The user cell holds a
 * real anchor, which restores keyboard access and open-in-new-tab without adding
 * any visible chrome — it renders as plain text (see `.rowLink`).
 */
function SessionRow({ session }) {
  const navigate = useNavigate();
  const href = `/sessions/${encodeURIComponent(session.id)}`;
  const name = displayNameFor(session);

  return (
    // Padding lives on the wrapper, not on Grid: Mantine's Grid applies negative
    // inner margins for the gutter, which would eat into its own padding.
    <Box p="md" className={classes.row} onClick={() => navigate(href)}>
      <Grid columns={4} gutter="md" align="center">
        <Grid.Col span={1}>
          <Group gap="sm" wrap="nowrap">
            <UserAvatar name={name} />
            <Anchor
              component={Link}
              to={href}
              className={classes.rowLink}
              fz="xs"
              lineClamp={1}
              onClick={(event) => event.stopPropagation()}
            >
              {name}
            </Anchor>
          </Group>
        </Grid.Col>

        <Grid.Col span={1}>
          <Stack gap={0}>
            <Text fz="sm" fw={700} lineClamp={1}>
              {session.connection || "—"}
            </Text>
            <Text fz="xs" c="dimmed">
              {session.type || "—"}
            </Text>
          </Stack>
        </Grid.Col>

        <Grid.Col span={1}>
          <Group gap="xs" wrap="wrap">
            {isLiveSession(session) && <LiveBadge />}
            <ReviewStatusBadge status={session.review?.status} />
            <WorkflowChip correlationId={session.correlation_id} />
          </Group>
        </Grid.Col>

        <Grid.Col span={1}>
          <Group gap={6} wrap="nowrap" justify="flex-end" c="dimmed">
            <Clock size={14} />
            <Text fz="xs">{formatFullDate(session.start_date)}</Text>
          </Group>
        </Grid.Col>
      </Grid>
    </Box>
  );
}

export default function SessionsList({ sessions }) {
  return (
    <Paper withBorder radius="md">
      {sessions.map((session) => (
        <SessionRow key={session.id} session={session} />
      ))}
    </Paper>
  );
}
