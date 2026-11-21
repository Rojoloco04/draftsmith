# Draftsmith plan

A personal OP.GG/U.GG for one player: my ranked stats dashboard, a meta tier list, and a champ select helper. This file is the source of truth for scope and decisions; update it as things change.

## Where things stand

Done (commits `09ccc8e`, `b544eaf`, and Phase 0 in `1de8202` on branch `phase0-verify`):

- `internal/riot`: Riot API client with a multi-window rate limiter (20/1s, 100/2m by default), 429 Retry-After handling, `ErrNotFound`/`ErrAuth` sentinels. Endpoints: account-v1 by Riot ID, match-v5 ids/match/timeline, league-v4 entries by PUUID.
- `internal/store`: raw match and timeline JSON archived gzipped under `data/`, plus state, daily rank snapshots, and an OP.GG response cache.
- `internal/ingest`: incremental sync. Pages the full match id list until a backfill completes (and every listed game is stored), then stops at the first fully archived page. Timelines Riot no longer has get a `.missing` marker.
- `internal/analysis`: per-game features (CS/gold/vision per min, KP, damage share, gold/CS/XP diff vs lane opponent at 10 and 15, deaths before 15, deaths on the enemy half, death positions) and a markdown report.
- `internal/opgg`: MCP client (Streamable HTTP, JSON or SSE responses, session header) for OP.GG's public server at `https://mcp-api.op.gg/mcp`.
- CLI: `sync`, `report`, `export`, `meta tools`, `meta call`, `version` (Phase 1 added `serve` and `reindex`).
- Tests use synthetic fixtures (`internal/fixture`), one scrubbed real game, and httptest fakes. Verified against the real Riot API and OP.GG in Phase 0.

Phase 1 (branch `phase1-dashboard`, commits `40330bf`, `c274e5c`, `410621f`; see the Phase 1 section):

- `internal/db`: SQLite index (`modernc.org/sqlite`, the first dependency) over the archive.
- `analysis.Summarize`: one computation shared by `report` and the dashboard.
- `internal/web` + `draftsmith serve`: the dashboard, with background sync. `draftsmith reindex` rebuilds the index.

The early code was stdlib only because the sandbox it was written in couldn't reach the Go module proxy. That constraint is gone; dependencies are fine where they earn their place.

Phases 0 and 1 were merged into `main` on 2026-11-17 and pushed to the public repo `github.com/rojoloco04/draftsmith`. Everything in it is public, so keep secrets, raw archive data, and other players' identities out of commits.

Phase 2 (branch `phase2-meta`, commits `f40577c`, `8c782f3`, `c40be78`; see the Phase 2 section): the OP.GG class-notation parser (`opgg.Parse`/`opgg.Decode`), `internal/meta` (typed, cached tier list, champion table and champion analysis), and the dashboard's tier list page, pool-vs-meta card and per-champion OP.GG card. Merged into `main` (fast-forward) and pushed on 2026-11-17.

The Phase 2 leftover (pool vs meta in the markdown `report`) is done on `main`: `meta.NewCacheOnly` serves whatever is cached however old and never dials OP.GG (it has no client, so a stray call would panic rather than hit the network), and `report` renders `meta.PoolMarkdown` after the champion pool, leaving the section out when no tier list is cached or there's no index yet (it won't create one).

The dashboard's Refresh button (sync now, reload when done) was merged into `main` on 2026-11-17 (not pushed).

Phase 3 was built on branch `phase3-watch` and merged into `main` on 2026-11-20 (see the Phase 3 section): client discovery, gameflow polling, reading champ select (checked against a real ranked champ select), lane odds for the enemy team, and suggestions with rune pages logged by `watch`.

UI refresh (on `phase3-watch`, 2026-11-17): new stylesheet (sticky header with icon nav, splash-art heroes on the overview, champion and match pages, rounded champion icons), ranked emblem and crest from CommunityDragon (`raw.communitydragon.org`, added to the CSP's `img-src`), role glyphs drawn as an inline SVG sprite in `layout.html`, and summoner spells, keystone and secondary tree on every match row and player line. That needed `spells` and `runes` columns on `participants`, so `db.SchemaVersion` is 2 and the first `serve` after upgrading rebuilds the index. Rune 8230 is now "Stormraider's Surge" (was Phase Rush) in Data Dragon. Checked by hand against the real archive in dark and light mode and at ~500px wide (headless Chrome works for screenshots; headless Edge on this machine exits without writing one, and desktop Chrome can't go narrower than ~500px, so true phone width needs DevTools emulation).

UI direction (from the user, 2026-11-17, sharpened 2026-11-20): they like the refresh as a baseline but want something bolder than a cleaner OP.GG clone. "Bold" means the visual design of the pages and interface: think outside the box and make it look cool and creative, not more stats. Picked 2026-11-20: a **death heatmap** across games on the rift and a **champion pool constellation**. Dropped: LP/form timelines and per-game story views.

The dashboard's champ select page is done (2026-11-18, see "Champ select page" in the Phase 3 section): `serve --watch` runs the watcher in the same process and the page follows it over SSE. Checked live against a real ranked champ select.

The overlay is built (2026-11-18, see "Overlay" in the Phase 3 section): `serve --overlay` docks a borderless WebView2 window beside the League client during champ select. The page is checked at 320px; the window's docking still needs a look in a real champ select.

**Next up (2026-11-20):** the death heatmap and the champion pool constellation, designed boldly (see UI direction). Then check the 2026-11-20 in-game features in a real game.

Prototypes (2026-11-20, asked by the user: "a few divergent prototypes, then pool the best parts"): three overview redesigns on the real `/` route, `?variant=A|B|C` (`0` is the current page), with a floating switcher. A War room (dark HUD, density heatmap with filters, polar pool sky), B Editorial (printed-issue layout, hex-binned small multiples by game phase, annotated pool scatter), C Hextech (game-client styling, ember heatmap with a time scrubber, champion web of back-to-back picks). Details and the verdict placeholder in `internal/web/PROTOTYPE_NOTES.md`. Waiting on the user's picks; then fold them in properly and delete the prototype files. Finding from real data: the pool is very wide (top champion 4 of 86 games, 52 champions), so any pool view has to handle many 1-2 game champions (adjusted WR only takes a few values there).

One command (2026-11-20, asked by the user): `draftsmith` with no arguments (or a double-click on the exe) runs `serve --open` from the exe's folder when `.env` isn't in the current one, and on an error waits for Enter so the console can be read. `serve` now watches the client and docks the overlay by default; `--no-watch` and `--no-overlay` turn them off (`--watch` and `--overlay` are gone). `--open` opens the dashboard in the browser. Self-rebuild (2026-11-20, asked by the user): a bare start from the checkout (`isCheckout`: `go.mod` is this module and `cmd/draftsmith` exists) runs `go build -o <exe> ./cmd/draftsmith`, compares the exe's SHA-256 before and after (an up-to-date build still touches the mtime), and on a change runs the new exe with `DRAFTSMITH_REBUILT=1` and exits with its code. Local source only, nothing is pulled. A failed build logs the compiler output and runs the old binary. Go renames the running exe to `draftsmith.exe~`; the next start deletes it (gitignored).

Icon (2026-11-20): `cmd/draftsmith/winres/icon.ico` is the dashboard logo drawn by `mkicon.go` (`go:build ignore`; it draws `icon.svg`'s shapes directly, no rasterizer needed). The user's desktop shortcut `Draftsmith.lnk` uses it. The exe itself has no embedded icon yet: that needs a `.syso` resource, e.g. from `go-winres`, which hasn't been approved to run here.

Earlier (asked by the user 2026-11-18, in this order):

1. ~~Summoner spell recommendations in champ select~~: done 2026-11-18, see "Spells and builds" in the Phase 3 section.
2. ~~Multiple accounts~~: done 2026-11-18, see "Multiple accounts" below. `Rojoloco#SLU` was added by the user (confirmed 2026-11-20).
3. ~~Build recommendations in game~~: done 2026-11-18, see "In-game build" below. The user answered the overlay question on 2026-11-20: the dashboard is enough during the game; the overlay stays champ select only. The overlay's docking was checked in a real champ select and is right.

Done 2026-11-20 (asked by the user, see "Full build, my hover, rune import, team comp, item tracking" below): the in-game card shows every build option, my hovered champion always gets a card with runes, an Import button puts a rune page in the client, team comp is in the score, and the in-game card tracks my items and level from the Live Client Data API. Not yet checked in a real game.

Multiple accounts (2026-11-18):

- One archive for every account: `state.json` is `{"accounts": [...]}`, each with its own PUUID, Riot ID, platform, backfill progress and last sync. Matches and timelines are shared (keyed by match id); rank snapshots live under `ranks/<puuid>/`. `LoadState` migrates the old single-account `state.json` (and moves `ranks/*.json` under the account) the first time the new build reads it. Checked on a copy of the real archive.
- `sync --riot-id Name#TAG` adds an account (or renames one whose PUUID is already tracked); plain `sync` and `serve`'s background sync go through every account. Accounts on another platform than `--platform` are skipped with a note.
- Index: `games.account` and `rank_snapshots.account` (PUUID), `SchemaVersion` 3. `Index` takes every PUUID; matches none of them played are retried each time, so adding an account indexes its games without a rebuild. Dropping one clears the index. `db.GameFilter.Account` filters ("" is all). `analysis.Game.Account` is `json:"-"`, so `export` doesn't write PUUIDs.
- Dashboard: with 2+ accounts the header pill is a menu (All accounts, then each). The choice is a cookie (`account`, the Riot ID), so links don't change; `/account?id=...&back=...` sets it and only redirects to local paths. Every game query filters by it. In "All", the overview lists each account's rank and the rank chart has one line per account on a shared day axis.
- `report` and `export` take `--account Name#TAG` (default `all`). Champ select suggestions use every account's solo queue games: it's the same player, and there are few games.
- Decided without asking (the user can overrule): "All accounts" is the default view, and suggestions pool every account.

In-game build (2026-11-18):

- `meta.Build` also has item names (`ids_names`, strings for items), `Skills` (level-by-level order) and `Max` (max order); analysis asks for them too (`analysisVersion` v4). `Build.String()` is the one-line form `watch` logs after lock-in ("; build Doran's Ring + Health Potion x2 > ... ; max Q > W > E").
- Each pick carries the build from the same response as its runes (the matchup guide when my laner is 50%+ likely, else champion analysis). The locked-in pick card shows it.
- `web`: when gameflow goes from ChampSelect to GameStart/InProgress/Reconnect with a locked pick on the board, the server keeps that pick (`champSelect.game`) and `/champ-select` and `/overlay` show an "In game" build card: starting items, boots, core, max order, the 15-level skill order, spells and runes. Any other phase clears it; a dodge never shows one; a game that started before `serve` saw its champ select shows no build.
- Static data only, for my own champion: no Live Client Data API yet, so nothing tracks what I've bought. The overlay window still shows only during champ select.

Full build, my hover, rune import, team comp, item tracking (2026-11-20):

- **Full build.** `meta.Build` has `Slots` (item options by build depth) and `Late` (items in finished builds). The matchup guide already returned them (`single_items` depth 1-6, 10 options each; `last_items`, 30); champion analysis now also asks for `fourth_items[]`, `fifth_items[]`, `sixth_items[]` and `last_items[]` (three options each), so `analysisVersion` is `v5`. Analysis still lists one option for start, boots and core; the matchup guide lists up to 15 core paths, so the in-game card is fullest when the lane opponent was 50%+ likely. `web.newGameBuild` lays it out: two starts, three core paths, three boots, four options each for the 4th to 6th items, six other late items, max order and skill order.
- **Runes for my hover.** The bug: a hover that wasn't a top-3 suggestion had no card, and my locked pick's rune fetch waited behind every matchup table in the serial OP.GG queue (1.5-2s each). Now `Live` has an urgent queue that runs first, used for my own champion (hover or lock); my hover always gets the first card (`Pick.Mine`, "Your hover"), scored if I've played it there at all; and while a matchup page loads, the usual page shows if it's already fetched. `Live.Reset` no longer runs on entering the game, so a rune page or build that lands after the game starts reaches the in-game card (`Server.WatchBoard` updates the game's pick when the champion matches).
- **Rune import.** One page, `lcu.PageName` ("Draftsmith Imported", the user's choice 2026-11-20), updated on every import, like other rune apps. `lcu.Client.ImportRunes`: deletes pages named `Draftsmith*` (only those, so older per-champion pages are cleaned up too), checks `/lol-perks/v1/inventory` `canAddCustomPage` when none was freed, then `POST /lol-perks/v1/pages` with `current: true` and 9 perk ids (4 primary, 2 secondary, 3 shards). Never overwrites my own pages; with no room it says to free one or rename one to start with Draftsmith. Web: `POST /champ-select/runes?champ=<key>` needs the `X-Draftsmith: import` header (cross-site pages can't send it without a preflight the server never answers); `serve --watch` sets the importer, which rediscovers the client per click. Button only; nothing imports by itself. The page limit and the exact "full" error text aren't checked against a real client yet.
- **Team comp.** `internal/draft/champions.json` is Data Dragon 16.23.1's champion table cut to tags and attack/defense/magic ratings. `draft.APShare` is 1 when magic > attack, 0 when attack > magic, 0.5 when tied, with a small override table for misreads (Bel'Veth, Ezreal, Akshan AD; Vex, Seraphine, Twisted Fate, Rell, Leona AP; Corki, Kai'Sa, Kog'Maw, Varus, Kayle, Jax, Shaco, Shen mixed). Frontline is Riot's Tank tag. `TeamComp.fit` adds `DamageWeight` (0.25) x 4 x (0.5 - team AP) x (pick AP - 0.5) in log odds, plus `FrontlineWeight` (0.15) for the team's first tank, scaled by known teammates / 4, and only with 2+ known (picks and hovers). The board shows "Your team: mostly AD · no frontline" and each pick's note. Decided without asking: the weights (about 6 and 4 points at most).
- **Item tracking.** `internal/liveclient` reads `https://127.0.0.1:2999/liveclientdata/activeplayer` (level, skill points, my Riot ID) and `playeritems?riotId=` for my items only; other players are never requested, and the Riot ID is never logged (errors carry the path only). TLS is verified against Riot's root without a hostname check (the game's cert may not name 127.0.0.1). `serve --watch` polls it every 2s while gameflow is GameStart/InProgress/Reconnect and stops after. The in-game card shows my items, "Buy next" (first missing item of the top core path until three build items are finished, then the top option for the next slot) and "Level up" (the skill order's next point when I have one to spend), with owned items ticked.

Handoff notes (things a new session would otherwise rediscover):

- Work on `main` and push there; no feature branches (the user's call, 2026-11-20).

- `gofmt -l` lists nearly every file because `core.autocrlf=true` gives CRLF working copies. That's noise; the repo stores LF. Run `gofmt -l` on files you touched, or check `gofmt -d` for real changes.
- Python's text mode on Windows writes CRLF; pass `newline='
'` when scripting edits to Go files, then `gofmt -w` them.
- `go build -o draftsmith.exe ./cmd/draftsmith` can leave a `draftsmith.exe~` behind; delete it, don't commit it.
- To check the dashboard against real data without calling Riot: `./draftsmith.exe serve --no-sync --addr 127.0.0.1:8099` (OP.GG is still called unless `--no-meta`). Stop it with `taskkill //F //IM draftsmith.exe` from Git Bash.
- "Adjusted win rate" everywhere is `(wins + 10 × overall) / (games + 10)` (`analysis.PriorWeight`); the user asked about it, so mention the formula if it comes up in UI copy.

Dev environment notes:

- Go 1.27.4 was installed with winget on 2026-11-17 (`C:\Program Files\Go\bin`). Shells started before that install don't have it on PATH; in PowerShell run `$env:Path = [Environment]::GetEnvironmentVariable("Path","Machine") + ";" + [Environment]::GetEnvironmentVariable("Path","User")` first. Git Bash may need a restart.
- `.env` is set up for Rojoloco#0419 on na1 with a working key. `data/` already holds the full archive, so `serve` has real data to show from the start.

## Phase 0: verify against real data (done 2026-11-17)

- Real Riot API works end to end: 51 solo queue games archived (Jul 2025 to Nov 2026, patches 15.14 to 16.22), all with timelines, 4 remakes. Current rank EMERALD IV.
- `riot/types.go` matches real payloads: no declared field missing in any match, `teamPosition` is always one of the five expected values, `gameDuration` is seconds, `frameInterval` is 60000, `CHAMPION_KILL` has `killerId`/`victimId`/`assistingParticipantIds`/`position`, and the league-v4 by-PUUID entry shape matches `riot.LeagueEntry`.
- Gotcha: `metadata.participants` is **not** in participantId order in real data. Nothing relies on it; keep it that way and always key off `info.participants[].puuid`.
- Every per-game feature (champion, result, KDA, CS, CS/min, KP, damage share, lane opponent, gold/CS diff at 10 and 15) matched an independent recompute from the raw JSON across all 47 non-remake games.
- OP.GG findings are in `docs/opgg-tools.md`. Short version: responses are a positional class notation, not JSON (needs a small parser); rates are rounded to 2 decimals, so compute `win / play`; the tier list has no tier/region/patch filter and is Emerald+; champion analysis takes a `tier` filter; the tier list uses display names, so join through `lol_list_champions`.
- One real game + timeline, scrubbed of every PUUID, Riot ID, summoner name and summoner ID, lives in `internal/fixture/testdata/` with `analysis.TestExtractRealGame` pinning its features.
- Still to eyeball by hand: the report's numbers against OP.GG's site for a couple of games.
- Windows note: PowerShell mangles JSON arguments to `meta call`. Use Git Bash, or read args from a file if this becomes annoying.

## Phase 1: dashboard (`draftsmith serve`) (done 2026-11-17)

Goal: an OP.GG-style site for me, served by the Go binary, reachable over Tailscale only.

What shipped, and where it differs from the plan below:

- Build order steps 1 to 5 are done. Overview, match history (htmx filters and paging), match detail, champions and per-champion pages work against the real archive; tier list and champ select are stub pages.
- **Charts are inline SVG rendered in Go** (`internal/web/chart.go`), not a vendored JS library. No script to load, tooltips via SVG `<title>`, and handler tests can assert on them. Revisit only if a chart needs real interactivity.
- **Icons come from Riot's Data Dragon CDN** (`ddragon.leagueoflegends.com/cdn/<patch>.1/...`), using each game's own patch. The URLs carry only champion and item ids. The CSP allows images from that host only; if an icon fails to load, the champion's initials show instead. `FiddleSticks` is the one archived name that differs from its Data Dragon id.
- Schema version lives in `PRAGMA user_version` (`db.SchemaVersion`). Bump it whenever stored columns or derived values change; `Open` drops every table and the next `Index` rebuilds them. The index also stores the tracked PUUID and clears itself if that changes, and reindexes a game whose timeline is archived after the match.
- `sync` updates the index too, so the CLI and `serve` never disagree. `serve` refuses `0.0.0.0`, `::` and a bare `:port`.
- Win rates are only colored green or red at 5+ games; below that the number is shown uncolored next to the game count.
- `analysis.Summarize` sorts ties by name, so report rows that tie on adjusted win rate are now in a stable order (they used to follow map order). Otherwise the report output is unchanged; checked by diffing old and new output on the real archive and on synthetic games.
- Verified by hand against the real archive: every page renders, real background sync runs, and match detail numbers match `analysis.TestExtractRealGame`.

Follow-ups, none blocking:

- Death heatmap across games (the per-game map exists; the `deaths` table is ready for an aggregate view).
- ~~Rate limit priority~~: not needed, Phase 4 is dropped.
- Still to eyeball by hand: the report's numbers against OP.GG's site for a couple of games (carried over from Phase 0).

Decided:

- **Storage: SQLite via `modernc.org/sqlite`** (pure Go, no cgo). The gzipped raw JSON under `data/matches` and `data/timelines` stays the source of truth; SQLite is a derived index that can always be rebuilt from it (`draftsmith reindex`, and automatically when the schema version changes). DB file at `data/draftsmith.db`.
- Server-rendered `html/template` + `embed`, htmx for interactivity (vendored, no CDN). No SPA build step. (Charts ended up as Go-rendered SVG; see above.)
- Bind to `127.0.0.1` by default, `--addr` to bind the Tailscale IP. No auth beyond that, so never bind `0.0.0.0` and never port forward.

Build order:

1. `internal/db`: open/migrate (a `schema_version` pragma or table), and tables for:
   - `games`: one row per match from `analysis.Game` (match id, start, patch, queue, remake, minutes, champion, role, opponent, win, K/D/A, CS, per-minute stats, KP, damage share, control wards, at10/at15 gold/CS/XP and diffs, deaths before 15, deaths on enemy half, has_timeline).
   - `deaths`: match id, minute, x, y (for a heatmap later).
   - `participants`: all 10 players' end-of-game stats per match for the match detail page. Store champion, team, position, K/D/A, CS, gold, damage, vision, items. **Do not store other players' names, Riot IDs, or PUUIDs**; show them as champion + position only.
   - `frames`: my and my lane opponent's per-minute gold/CS/XP for the detail page curve.
   - `rank_snapshots`: date, queue, tier, division, LP, wins, losses (imported from `data/ranks/*.json`).
   - `meta_cache`: key, fetched_at, patch, body (Phase 2 fills this; replaces `data/meta/*.json`).
2. Indexing: after each sync, insert any archived match not yet in `games` (cheap: compare ids). Reuse `analysis.Extract`; don't duplicate feature logic.
3. Make the report's aggregates reusable. `analysis/report.go` currently builds aggregates and writes markdown in one function (`WriteReport`, with the `agg` type, `groupBy`, buckets, `observations`). Split it into a `Summary` struct computed from `[]Game` (by season, by role, champion rows with adjusted WR, gold@15 buckets, matchups, recent form, notes) plus a markdown renderer, so the dashboard and `report` share one computation. Keep `report` output unchanged; the existing report tests should still pass.
4. `internal/web`: handlers + templates + static files, all embedded.
5. `draftsmith serve` in `cmd/draftsmith`: flags `--addr` (default `127.0.0.1:8080`), `--sync-every` (default 25m), `--no-sync`. Background sync calls `ingest.Sync` the same way `cmdSync` does (factor the shared setup out of `cmdSync` rather than copying it), then reindexes and snapshots rank. Log to stdout.
6. Rate limit priority: the riot `Limiter` is one shared FIFO-ish limiter today. My sync is the only Riot consumer until Phase 4 (`sample`), so priority can wait until then; note it, don't build it yet.

Pages (in order of value):

- **Overview**: rank + LP history (from daily snapshots; only exists from 2026-11-17 on), win rate trend (rolling 10/20), role split, top champions, "what stands out" notes.
- **Match history**: paginated list with champion, role, KDA, CS/min, result, gold diff @15, opponent; filters by role/champion/queue via htmx. Detail view: both teams' end-of-game stats (champion + position only, no names) and my gold/CS curve vs my lane opponent.
- **Champions**: my per-champion table (games, raw and adjusted win rate, KDA, CS/min, lane diffs), drill-down to matchups and that champion's games.
- **Tier list** and **Champion page (OP.GG side)**: need the Phase 2 parser; stub the nav links until then.
- **Champ select**: live view fed by `watch` (Phase 3), via SSE.

Testing: handlers with `httptest` against a temp SQLite DB built from `internal/fixture` (synthetic + the real scrubbed game). Run `gofmt`, `go vet ./...`, `go test ./...` before each commit.

Small-sample caveat: there are only 47 real games, so per-champion and matchup views should show game counts prominently and use the adjusted win rate by default.

## Phase 2: OP.GG meta integration (dashboard side done 2026-11-17)

What shipped:

- `opgg.Parse` turns class notation into plain Go values; `opgg.Decode` fills json-tagged structs from it and returns `*opgg.FieldError` when the server reports requested fields it skipped (`_field_diagnostics`), so schema drift is an error rather than silently empty data. Plain JSON responses go through the same functions. Tests use real responses captured on patch 16.22 (`internal/opgg/testdata`).
- `internal/meta`: `TierList` (one `position: all` call, joined to Riot keys through `lol_list_champions`), `Champions`, `Analysis` (stats, runes, damage type, three best and worst matchups, patch from `data.trends.win.version`), and `PoolFit`. Raw response text is cached in `meta_cache` and parsed on read. Fresh for 24h (7 days for the champion table); after that a refresh is tried, and on failure the old copy is served with `RefreshErr` set and no retry for 5 minutes. Each refresh opens a new MCP session, so a session the server dropped during a long `serve` can't get stuck. Network calls are serialized.
- Dashboard: `/tier-list` (per position, defaulting to my main role; sortable; my adjusted win rate and record next to each champion), a "your pool vs the meta" card and a Meta tier column on `/champions`, and an OP.GG card on each champion page with my record against its listed matchups. `serve --no-meta` turns it all off; `serve` warms the tier list cache at startup. Pages wait at most 15s on OP.GG.
- Checked against live OP.GG and the real archive: every page renders, cached loads take about 5ms, and Wukong (`MonkeyKing`) and Riot's `FiddleSticks` spelling join correctly.

Decided:

- **Champion analysis is cached for 24h, not per patch.** The tier list has no patch field, and a daily refresh catches patch day anyway. The analysis's patch is stored and shown.
- "Working for you" and "losing for you" in the pool card use the report's threshold: adjusted win rate 3+ points above or below my overall. Meta strong is tier 1-2, weak is tier 4-5. The notes need 3+ games; the Meta column shows every champion.
- In both counter lists, `win` is the analyzed champion's wins (checked against `my_win_rate`), so `Counter.WinRate()` is always "my champion into them".
- The champion table's `Jade_*` mode variants (ids 60001+) are skipped.
- Pool vs meta in the markdown `report` (after Phase 2 merged): reads only `meta_cache` through `meta.NewCacheOnly`, flags a tier list older than a day, and is skipped when nothing is cached. Same 3+ game rule and thresholds as the dashboard card.

Left:

- ~~Rune names and icons~~: done with the UI refresh (below). `internal/web/ddragon` embeds Data Dragon's `runesReforged.json` and `summoner.json`; the champion page's OP.GG card shows the most played rune page as icons.
- `lol_get_lane_matchup_guide` isn't wrapped yet; Phase 3's matchup view is the first real use.
- ~~The `data/meta/*.json` probe files~~: removed 2026-11-20. `meta call` only prints now; the store no longer creates `meta/`.

Original plan:

- `internal/meta`: typed wrappers over the OP.GG tools once Phase 0 shows the schemas. Cache each response with a timestamp and patch; serve stale data if OP.GG is down and show its age.
- First piece: a parser for OP.GG's class notation into `map[string]any`/typed structs, tested on responses captured in `docs/opgg-tools.md` probes.
- Tier list: one `position: all` call per day (no bracket filter; it's Emerald+). Rank by `win / play`, not the rounded `win_rate`.
- Champion analysis: fetch on demand, cache per patch.
- Join meta into my champion pool: my adjusted win rate next to the champion's current tier, so the report can say "strong for you and strong in the meta" vs "you're good on it but it's weak this patch".
- Risk: this is an agent-facing endpoint, not a supported data API. Keep all OP.GG parsing in one package so a schema change is a one-file fix.

## Phase 3: champ select helper (`draftsmith watch`)

Runs on the PC where League is played, because the League Client API (LCU) only listens on localhost.

Done so far (branch `phase3-watch`, 2026-11-17):

- `internal/lcu`: `Discover` reads `<install dir>/lockfile` (default `C:\Riot Games\League of Legends`, `--league-dir`/`DRAFTSMITH_LEAGUE_DIR` to override), then falls back to `LeagueClientUx.exe`'s command line through PowerShell CIM, keeping only `--app-port`, `--remoting-auth-token` and `--app-pid`. That command line also carries Riot Client tokens (including a base64 settings blob), so never print or log it.
- TLS: the client's certificate (CN `rclient`, SAN `localhost` and `127.0.0.1`) is issued by Riot's published root (`internal/lcu/riotgames.pem`, SHA-256 `ca8c9d32...4ea3`, pinned in a test). Go's normal verification with that root as the only trusted CA works; nothing is skipped. curl on Windows (schannel) fails with a revocation check error, which is curl's problem, not the cert's.
- `internal/watch` + `draftsmith watch`: polls `gameflow-phase` every 2s, reads `/lol-champ-select/v1/session` every 1s during ChampSelect, logs a one-line summary when anything but the countdown changes, and rediscovers the client after it closes or restarts. `--capture DIR` writes each (filtered) session plus the raw payload's key names without values.
- `lcu.Session` declares only allowed fields, so decoding drops identity. Checked against a real ranked champ select (queue 420): every declared field exists under that name, and the captured files hold no identity fields. Two filtered snapshots are test fixtures in `internal/lcu/testdata`.

Real client findings:

- Phases seen: `Lobby`, `Matchmaking`, `ReadyCheck`, `ChampSelect`, `InProgress`. Timer phases: `PLANNING`, `BAN_PICK`, `FINALIZATION`, `GAME_STARTING`. Outside champ select the session is a 404 (`"No active delegate"`).
- **Ranked leaves `bans.myTeamBans`/`theirTeamBans` empty.** Bans are only in `actions`, as completed `ban` actions (one per player, simultaneous), then a `ten_bans_reveal` action. Enemy ban actions read champion `0` until the reveal; `Session.BanList` also only takes enemy bans after the reveal completes, so nothing shows that the client doesn't.
- `championId` is the hover during a player's pick turn and the pick after it; locked in means a completed `pick` action for that cell (`Session.Locked`). `championPickIntent` is the hover before the turn. Enemy hovers during their turn are visible, as they are in the client.
- `myTeam` is reordered mid draft, so sort by `assignedPosition` (`top`, `jungle`, `middle`, `bottom`, `utility`) for display. `theirTeam[].assignedPosition` is always `""` in ranked.
- Other non-identity fields worth having later: `myTeam[].isAutofilled` (now read), `queueId` (now read), `actions[][].pickTurn`, `timer.isInfinite`, `trades`/`positionSwaps`/`pickOrderSwaps` (cell + state only).

Suggestions (`internal/draft`, 2026-11-17):

- **Decided: no Jev (or any LLM) in the loop.** The user asked about TypeSafe's Jev decision model. The probabilities it would guess are already in OP.GG's data, and exact arithmetic over them is offline, instant, deterministic and explainable; Jev is paid, networked, can vary between identical calls, and returns no reasons. Revisit only for something genuinely fuzzy that the data can't answer.
- **Lane odds** (`draft.Roles`): all 120 lane assignments for the enemy team, each weighted by the product of the champions' `role_rate` in their lanes (P(lane | champion) and P(champion | lane) rank assignments the same). Lanes the tier list cuts off share whatever `role_rate` the listed lanes leave, at least 0.5% each. Open slots fit any lane. Enemy hovers count as picks (the client shows them).
- **Score** (`draft.Suggest`), for each champion with 2+ of my solo queue games in my lane that isn't banned or locked: my record blended with 10 games at (my base + its OP.GG win rate − 50%), then shifted in log odds by Σ P(enemy is my laner) × (logit matchup win rate − logit its usual win rate). Matchups come from OP.GG's table blended with 500 games at its usual rate. Unpicked enemies count as an average opponent. Teammates' hovers are flagged, not excluded.
- **OP.GG matchups** (`meta.Matchup`, `lol_get_lane_matchup_guide`, plain JSON): `data.counters` is the champion's whole matchup table in that lane (53 rows for Galio mid), the same whatever opponent is asked, and `win` is the table owner's wins (Galio into Katarina 2191 of 4213; Katarina's table has Galio at 2022 of 4213). So one mirror request per pool champion (`MatchupTable`, smallest response, no runes) gives every matchup. Runes in the guide are matchup specific and come with names. `opponent_champion: NONE` is an error. Calls take 1.5-2s, so `watch` prefetches my main lane's tables at start and fetches everything else in the background.
- **Runes**: the top matchup page when my laner is 50%+ likely, else the champion's usual page from champion analysis (which now also asks for rune names; its cache key has a version, `analysisVersion`, so older copies are re-fetched). Tree and stat shard names are a small fixed table in `meta`. Nothing is imported into the client yet; that stays button-only.
- `watch` output: the ranking as a block when it changes, then one line per rune page as it arrives. After I lock in, only my champion's page for the likely matchup.

Champ select page (2026-11-18):

- **Same process, decided:** `serve --watch` (with `--league-dir`, `--min-games`) runs the watcher inside `serve`, since the archive, `.env` and League all live on this PC. That settles the open decision below for `serve` and `watch`; Quartermaster is still open. `draftsmith watch` stays as the terminal version and shares its setup (`newWatcher` in `cmd/draftsmith/watch.go`).
- `draft.Live.OnBoard` gets a `draft.Board` whenever anything on it changes (compared by a `%+v` fingerprint, since boards hold NaNs): phase, the countdown's end, both teams (mine in lane order, theirs with each slot's likeliest lane from `draft.Roles`), bans, and up to three `Pick`s with numbers, matchups and rune state (`loading`/`none`/`ok`), or just mine once locked. The log output is unchanged.
- `web`: `Server.WatchStarted/WatchPhase/WatchBoard` feed it; `/champ-select` renders it and `/champ-select/events` streams the rendered `board` fragment as an SSE event on connect and on every change, with a keep-alive comment every 20s. `app.js` swaps the fragment in, keeps folded/open rune pages as they were, counts the timer down locally, and dims the board while `EventSource` reconnects. A board that arrives outside ChampSelect (a late rune fetch) is dropped; leaving ChampSelect clears it.
- Plumbing: the stream clears the server's 60s write deadline with `http.ResponseController` (the request logger's recorder has `Unwrap` for this), and `serve` sets `BaseContext` so open streams end on shutdown instead of holding it for the 5s timeout.
- `lcu.Timer` now reads `isInfinite` (no countdown when set).
- Checked by hand during a real ranked game: bans, lane odds, locked pick with matchup runes, countdown, and the switch to "In game" when it loaded. Headless Chrome screenshots need `--timeout=3000`, not `--virtual-time-budget` (the open event stream crashes the renderer with the latter).

Spells and builds (2026-11-18):

- `meta.Build` holds summoner spells, starter items, boots and core items as `meta.Choice` (ids, play, win, pick rate). The lane matchup guide already returned all of them (several options each, for that matchup); champion analysis now asks for them too and returns one option each (the most played), so `analysisVersion` is `v3`. The analysis's `ids_names` for spells are ids, not names, so spell names come from a fixed table (`meta.SpellName`), like the rune trees.
- Each pick's spells come with its rune page from the same response: the matchup's top pair when my laner is 50%+ likely, else the champion's usual pair. `watch` logs them after the page ("; spells Flash + Teleport"); the pick card shows a Spells row with the pick rate.
- The guide also has `skills` (level-by-level order), `last_items`, `single_items` and `combination_items` by build depth; the in-game build can use those.
- Test fixture `internal/opgg/testdata/analysis_galio_mid_build.txt` is a real v3 analysis (patch 16.23).

Overlay (2026-11-18):

- `serve --overlay` (implies `--watch`) opens `internal/overlay`: a WebView2 window (`github.com/jchv/go-webview2`, pure Go; there's no C compiler here, so cgo bindings are out) showing `/overlay`, the champ select board without the dashboard around it. My team is left out (the client shows it); the enemy team is a strip with lane odds on top, then the picks. Shown only while gameflow is ChampSelect.
- Docking: 320 logical px wide (a 1280x720 client centered on 1080p leaves exactly that each side), as tall as the client, to its right if the monitor's work area has room, else its left, else over its right edge (`overlay.Dock`, tested). Scaled by the client monitor's DPI; the window thread is per-monitor DPI aware v2.
- The client's window is found by owning process (`LeagueClientUx.exe`, the largest visible top-level window), not class or title. Hidden when the client is minimized or hidden (in game it goes to the tray).
- Z-order: topmost while the client or the overlay is in front; otherwise dropped just behind the foreground window, so it doesn't float over other apps. Shown with `SWP_NOACTIVATE`, so it never takes focus by itself. Borderless popup with `WS_EX_TOOLWINDOW` (no taskbar button or Alt+Tab entry).
- The × in its bar calls the bound `window.draftsmithHide` and hides it until the next champ select; in a browser the button stays hidden. Alt+F4 on it closes it for the rest of that `serve`.
- go-webview2's `edge` package locks the main goroutine to the main thread and calls `CoInitializeEx` in `init`. The overlay runs on its own locked thread with its own `CoInitializeEx`, so `serve` keeps its structure. The library briefly shows a framed window at startup before the overlay hides it. WebView2's profile lives in `%LocalAppData%\draftsmith\webview2`.
- Checking it: headless Chrome won't make a window narrower than ~500px even with `--force-device-scale-factor`; for a true 320px viewport, drive DevTools `Emulation.setDeviceMetricsOverride` (a small Node script over the `--remote-debugging-port` WebSocket works; Node 24 has `WebSocket` built in). Don't screenshot the desktop to check the window: the user may be in a game or have private apps open.
- Never inject into or modify the client's own UI (client plugin loaders): that's outside what Riot allows and risks the account. A separate window doesn't touch the client.

Plan from here:

- Discover the client: read the `lockfile` in the League install dir (`LeagueClient:pid:port:password:https`), fall back to `LeagueClientUx.exe` process args (`--app-port`, `--remoting-auth-token`). Basic auth `riot:<password>`. The cert is self-signed by Riot; pin Riot's root cert rather than disabling verification globally.
- Start with polling (`GET /lol-gameflow/v1/gameflow-phase`, then `GET /lol-champ-select/v1/session` about once a second during ChampSelect). The WebSocket event stream is nicer but needs a dependency; only add it if polling feels laggy.
- From the session use only: my cell and assigned position, my team's picks and hovers, enemy picks once visible, bans, and the action timer. Verify field names against the real client.
- Suggestions: score champions from my pool for my assigned role using my adjusted win rate and games played, OP.GG tier, OP.GG counter data vs visible enemy laner, and team comp gaps (damage type, frontline). Show the top 3 with one-line reasons.
- Runes: show OP.GG's recommended page for the suggested or hovered champion. Importing it into the client (`/lol-perks/v1/pages`) only happens when I click a button: done 2026-11-20; the page limit still needs a look on the real client.
- ~~Push state to `serve`~~: done in process with `serve --watch` (see "Champ select page").

### Compliance rules (non-negotiable)

- Never show or look up other players' names, Riot IDs, ranks, or stats during champ select, including teammates outside my party. Riot policy: products cannot de-anonymize players who can't be identified from visible information. Ignore any identity fields in the session payload; don't log them.
- Suggestions only. Never hover, pick, ban, or lock in automatically ("apps that dictate player decisions" are not allowed).
- In game (relaxed 2026-11-18 at the user's request, was "nothing during the game itself"): only static build recommendations for my own champion, of the kind Riot-approved apps show. No enemy cooldown, summoner spell or jungle timers, no tracking of anything the game doesn't show, and no player lookups. Riot's Live Client Data API (`https://127.0.0.1:2999`) is fine for reading my own champion and items.
- No League Classic data, ever.
- Personal key means personal use: don't expose the dashboard publicly.

## Phase 4: elo benchmarks (`draftsmith sample`): dropped

Dropped 2026-11-20: the user finds it overkill; OP.GG meta stats are enough. The original idea, kept for reference:

- Low priority background job using only spare rate limit. Pull players from my tier and division via league-v4, a handful of recent ranked games each, with timelines.
- Store aggregates only: per role, distributions (median, p25, p75) of CS diff at 10, gold diff at 15, vision/min, deaths before 15, KP, damage share. No per-player records kept.
- A few thousand games is enough. Then the report and dashboard can say "below your bracket", not just "below your own average".

## Phase 5: Quartermaster: not planned

Not planned (2026-11-20). Quartermaster runs on the same PC as League. The most the user might want, someday, is asking Quartermaster for League stats in their Discord server. That would be a read-only call to the dashboard's HTTP API, not a module. The original idea, kept for reference:

- Post-game summary to Discord after each ranked game, weekly trend digest, `/draftsmith` slash command for current form.
- Either Quartermaster calls Draftsmith's HTTP API, or Draftsmith becomes a Quartermaster module. Decide after Phase 1 is stable.

## Open decisions

- ~~**Does Quartermaster run on the same PC as League?**~~: yes (2026-11-20). No integration planned; see Phase 5.
- ~~**Storage for the dashboard**~~: resolved 2026-11-17, SQLite (`modernc.org/sqlite`) as a rebuildable index over the raw JSON archive. See Phase 1.
- ~~**OP.GG tier filter**~~: resolved. The tier list is fixed at Emerald+ (fine for me); per-champion analysis accepts `tier` (e.g. `emerald_plus`, `master_plus`).
- **Riot product approval**: personal key registered as "Draftsmith"; if Riot asks for changes, update this file.

## Conventions

- Run `gofmt`, `go vet ./...`, `go test ./...` before every commit.
- Keep raw Riot JSON untouched in the archive; derive everything else from it.
- No secrets in the repo, logs, or fixtures.
- Commit identity: rojoloco04 <parrackjack@gmail.com>.
