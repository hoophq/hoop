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

## Trial 2 · ENG-596 (2026-10-14)

A UI ticket: paging and user counts on the Access Control page.

| Version | What the agent delivered | Why it did not work | Rule it produced |
|---|---|---|---|
| 1 | 3 PNG comparisons, one of them a new users-per-role table | Seeing the table, the human removed it and its endpoint, after two QA rounds | Show a new view's layout before building its backend |
| 2 | The same PNGs after the cut; one taken from QA round 3 | That capture showed a bug fixed after the round (page 1 started at `pg-1936`) | Record on the final commit and look at the frames |
| 3 | One side-by-side GIF/MP4 of the same scripted flow, with a request counter and click rings | Accepted: "by far the best evidence so far" | A UI change is shown as a recorded flow |

Also found on the way:
- One overlay number (requests on load: 21 on main, 0 on the branch) replaced
  a whole PNG of request logs. Put what the page hides on screen, labeled
  "test overlay".
- A panel left open hid the filter result in the last frame. Undo before the
  last step.
- QA's stacks were still up with QA's seed; recording there cost nothing. The
  gateway cached `index.html`, so a UI swap needed a container restart.

## Trial 3 · ENG-611 (2026-10-16)

The UI part was one sentence in the reject dialog; the behaviour it describes
(a held client reads the approver's reason) happens outside the browser.

| Version | What the agent delivered | Why it did not work | Rule it produced |
|---|---|---|---|
| 1 | Side-by-side video: open the approval, click Reject, type a reason | "It does not help at all": both videos showed the same clicks; only one sentence differed | A video must show a difference between the two sides, not just the screen the diff touched |
| 2 | Video dropped, the sentence quoted in `EVIDENCE.md` | Wrong fix: the human wanted a better video, not none | Improve an item that misses; never drop it |
| 3 | The real flow: a psql held on the sidecar, the approver rejects in the web app, the psql output mirrored live in a "test overlay" panel | Accepted: "the CLI over the UI was incredible" | When a UI action lands outside the browser, put that effect on screen, live |

Also found on the way:
- The overlay is fed by the real client's output file through a local CORS
  file server, so it shows what the client printed, not a mock. Labeled
  "test overlay" so nobody mistakes it for product UI.
- Main showed "was rejected (approval …)", the branch added the reason: the
  difference sits in one line at the bottom of the frame, and the last frame
  holds it.
