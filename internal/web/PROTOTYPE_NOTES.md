# Overview redesign prototype (throwaway)

**Question:** what should the bolder dashboard look like, and how should the death heatmap and the champion pool constellation be drawn? (PLAN.md, "UI direction", 2026-11-20.)

Open the overview with `?variant=0|A|B|C` (0 is the current page). A floating bar at the bottom, or the ← / → keys, cycles through them. Real data, real header.

| Key | Name | Page | Death heatmap | Pool constellation |
| --- | --- | --- | --- | --- |
| A | War room | Dark HUD, monospace, clipped corners, scanlines; rank, last 20 and stats in a left rail | Smooth density over a desaturated Rift with filters (phase, wins/losses, my side vs as played) and a readout (enemy half %, median minute, hottest zones) | Polar "pool sky": sector = role, distance from the core = adjusted WR, size = games, each role's stars joined best first |
| B | Editorial | A printed issue: giant serif rank headline, deck sentence, numbered sections, greyscale splash portrait | Hex-binned small multiples for laning / mid / late game on line-art Rifts, with a caption naming the worst zone | Annotated scatter: games across, adjusted WR up, quadrants (signature picks, hidden weapons, comfort traps, experiments), beeswarm for one-off picks, callouts on the most played |
| C | Hextech | Game-client feel: gold frames, splash backdrop, diamond stat gems, hex portraits | Glowing embers (teal = won game, crimson = lost) with a time scrubber and play button, plus ranked "danger zones" | Champion web: most played champions in a ring by role, threads between champions played back to back |

Shared bits: red side games are flipped so my base is always bottom left (B and C always, A by toggle); zones are named from coordinates (lane, river, own/enemy jungle, base).

Files: `prototype_overview.go`, `templates/prototype_overview.html`, `templates/prototype_switch.html`, `static/prototype.css`, `static/prototype.js`, `../db/prototype_deaths.go`, plus small hooks marked PROTOTYPE in `pages.go`, `server.go` and `templates/overview.html`.

## Verdict

_To fill in with the user: which parts from which variant ("A's heatmap with B's page and C's embers scrubber", etc.)._

Then fold the picks into the real overview (with tests: a proper `db` deaths query, handler tests) and delete every file above and the PROTOTYPE hooks.
