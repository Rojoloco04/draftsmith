# Visual skins prototype (throwaway)

**Question:** with the overview's features settled (2026-11-20), what should the whole dashboard look like? Appearance only: every skin shows exactly the same pages, data and features.

Open any page and use the floating bar at the bottom, or the ← / → keys, to switch. Switching is instant and remembered across pages (localStorage), so you can browse the whole dashboard in one skin. `?skin=forge|broadsheet|aurora|blocks` picks one from a link; `?skin=` goes back to the current look.

| # | Skin | Look |
| --- | --- | --- |
| 0 | Current | Today's stylesheet, following the OS light/dark setting |
| 1 | Forge | Midnight navy and gold: serif small caps headings, cut-corner gilded panels with a gold crest line, faint hex pattern, teal wins, gold underline nav |
| 2 | Broadsheet | Newsprint: warm paper, ink type, big serif headlines, rules instead of boxes, greyscale art, one red accent, no shadows or rounded corners |
| 3 | Aurora | Frosted glass over a purple, teal and pink night-sky gradient: big radii, gradient headings, glowing pills, round stat gems |
| 4 | Blocks | Neo-brutalist: yellow header, thick black borders, hard offset shadows, heavy uppercase type, flat blue/red/yellow, square portraits |

Each skin is one color scheme (Forge and Aurora dark, Broadsheet and Blocks light); the winner can get its other half when it's folded in.

Files: `static/prototype-skins.css`, `static/prototype-skins.js`, and the PROTOTYPE hook in `templates/layout.html`. The overlay window doesn't load them.

## Verdict

_To fill in with the user: which skin, or which parts of which (e.g. "Forge's panels with Aurora's glow and Blocks' header")._

Then fold the picks into `style.css` (both light and dark), and delete the files and hook above.
