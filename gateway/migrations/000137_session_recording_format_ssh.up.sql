-- ENG-587: SSH sessions recorded since 000126 were marked raw. Their events
-- are hoop SSH frames, so the viewer can replay their pty channels.
UPDATE private.sessions
SET recording_format = 'ssh'
WHERE recording_format = 'raw'
  AND verb = 'connect'
  AND connection_type = 'application'
  AND connection_subtype IN ('ssh', 'ssh-local', 'git', 'github');
