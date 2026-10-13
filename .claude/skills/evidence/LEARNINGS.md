# Evidence process: what each trial taught

Each rule in `SKILL.md` exists because a trial failed without it.

## Trial 1 · ENG-525 (2026-10-06)

| Version | What the agent delivered | Why it did not work | Rule it produced |
|---|---|---|---|
| 1 | 48 raw files in one folder (`.json`, `.txt`, `.exit`), 15 MB | No story, no link to the ticket, only on one laptop | Raw files are not evidence |
| 2 | Same files in `raw/round*`, plus an 8-section `REPORT.md` | Still a report a person must trust or audit by hand; images buried under 100+ files | Evidence is pictures of a real surface, not text about files |
| 3 | Real stack: API response, sidecar log and rule screen on `main` and branch | Right content, but as 6 separate GIFs and PNGs | — |
| 4 | 3 comparison images (BEFORE · AFTER · DURING DEPLOY) + one-page `EVIDENCE.md` | Accepted | Compose side by side; a GIF of typing is a still image; one image per question |

Also found on the way:
- Comparing only the headline (served sidecar config) hid a difference in the
  rest of the state (a rule's connection list). Compare the whole state; show
  the reader the surface that answers each question.
- The cross-version check that found two real bugs lived as a `.txt` in the
  evidence folder. It became a test in the PR. Re-runnable proof belongs in CI.
- A first real-stack run overlapped two processes on one port and one database
  and had to be discarded. One process at a time; check the new one answers.
- For a change where "nothing visible changes" is the goal, say so in the first
  line, and name the ticket where the visible part arrives.
