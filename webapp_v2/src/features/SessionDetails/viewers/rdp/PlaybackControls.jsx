import { useState } from 'react'
import { Group, Slider, Stack, Text } from '@mantine/core'
import { FastForward, Maximize, Minimize, Pause, Play, Rewind } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Button from '@/components/Button'
import { formatClock } from './rdpLogic'
import classes from './Rdp.module.css'

const SPEEDS = [0.5, 1, 2, 4]

// Seek bar, frame steps, Play/Pause, time, speed and full screen (matrix P04).
export default function PlaybackControls({
  time,
  duration,
  playing,
  speed,
  fullscreen,
  onPlay,
  onPause,
  onPrev,
  onNext,
  onSeek,
  onSpeed,
  onFullscreen,
}) {
  // The position under the pointer while dragging; the seek happens on release.
  const [scrub, setScrub] = useState(null)
  const shown = scrub ?? Math.min(time, duration)

  return (
    <Stack gap="xs" p="md" className={classes.controls}>
      <Slider
        aria-label="Playback position"
        size="sm"
        min={0}
        max={duration || 0}
        step={0.01}
        value={shown}
        label={null}
        onChange={setScrub}
        onChangeEnd={(value) => {
          setScrub(null)
          onSeek(value)
        }}
      />
      <Group justify="space-between" wrap="nowrap">
        <Group gap="xs" wrap="nowrap">
          <ActionIcon variant="light" color="gray" aria-label="Previous frame" onClick={onPrev}>
            <Rewind size={16} />
          </ActionIcon>
          <Button
            leftSection={playing ? <Pause size={16} /> : <Play size={16} />}
            onClick={playing ? onPause : onPlay}
          >
            {playing ? 'Pause' : 'Play'}
          </Button>
          <ActionIcon variant="light" color="gray" aria-label="Next frame" onClick={onNext}>
            <FastForward size={16} />
          </ActionIcon>
        </Group>
        <Group gap="md" wrap="nowrap">
          <Text size="sm" c="dimmed">
            {`${formatClock(shown)} / ${formatClock(duration)}`}
          </Text>
          <Group gap={4} wrap="nowrap" role="group" aria-label="Playback speed">
            {SPEEDS.map((value) => (
              <Button
                key={value}
                size="compact-xs"
                color="gray"
                variant={value === speed ? 'filled' : 'light'}
                aria-pressed={value === speed}
                onClick={() => onSpeed(value)}
              >
                {`${value}x`}
              </Button>
            ))}
          </Group>
          <ActionIcon
            variant="light"
            color="gray"
            aria-label={fullscreen ? 'Exit full screen' : 'Full screen'}
            onClick={onFullscreen}
          >
            {fullscreen ? <Minimize size={16} /> : <Maximize size={16} />}
          </ActionIcon>
        </Group>
      </Group>
    </Stack>
  )
}
