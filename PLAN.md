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

**Next up: Phase 2** on a new branch off `main`, starting with the OP.GG class-notation parser (see Phase 2 and `docs/opgg-tools.md`).

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
- Rate limit priority still waits for Phase 4 (step 6 below).
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

## Phase 2: OP.GG meta integration

- `internal/meta`: typed wrappers over the OP.GG tools once Phase 0 shows the schemas. Cache each response with a timestamp and patch; serve stale data if OP.GG is down and show its age.
- First piece: a parser for OP.GG's class notation into `map[string]any`/typed structs, tested on responses captured in `docs/opgg-tools.md` probes.
- Tier list: one `position: all` call per day (no bracket filter; it's Emerald+). Rank by `win / play`, not the rounded `win_rate`.
- Champion analysis: fetch on demand, cache per patch.
- Join meta into my champion pool: my adjusted win rate next to the champion's current tier, so the report can say "strong for you and strong in the meta" vs "you're good on it but it's weak this patch".
- Risk: this is an agent-facing endpoint, not a supported data API. Keep all OP.GG parsing in one package so a schema change is a one-file fix.

## Phase 3: champ select helper (`draftsmith watch`)

Runs on the PC where League is played, because the League Client API (LCU) only listens on localhost.

- Discover the client: read the `lockfile` in the League install dir (`LeagueClient:pid:port:password:https`), fall back to `LeagueClientUx.exe` process args (`--app-port`, `--remoting-auth-token`). Basic auth `riot:<password>`. The cert is self-signed by Riot; pin Riot's root cert rather than disabling verification globally.
- Start with polling (`GET /lol-gameflow/v1/gameflow-phase`, then `GET /lol-champ-select/v1/session` about once a second during ChampSelect). The WebSocket event stream is nicer but needs a dependency; only add it if polling feels laggy.
- From the session use only: my cell and assigned position, my team's picks and hovers, enemy picks once visible, bans, and the action timer. Verify field names against the real client.
- Suggestions: score champions from my pool for my assigned role using my adjusted win rate and games played, OP.GG tier, OP.GG counter data vs visible enemy laner, and team comp gaps (damage type, frontline). Show the top 3 with one-line reasons.
- Runes: show OP.GG's recommended page for the suggested or hovered champion. Importing it into the client (`/lol-perks/v1/pages`) only happens when I click a button. Verify endpoint behavior and the page limit on the real client.
- Push state to `serve` (same process if Quartermaster runs on this PC, otherwise HTTP over Tailscale).

### Compliance rules (non-negotiable)

- Never show or look up other players' names, Riot IDs, ranks, or stats during champ select, including teammates outside my party. Riot policy: products cannot de-anonymize players who can't be identified from visible information. Ignore any identity fields in the session payload; don't log them.
- Suggestions only. Never hover, pick, ban, or lock in automatically ("apps that dictate player decisions" are not allowed).
- Nothing during the game itself. The watcher goes idle when gameflow leaves ChampSelect.
- No League Classic data, ever.
- Personal key means personal use: don't expose the dashboard publicly.

## Phase 4: elo benchmarks (`draftsmith sample`)

- Low priority background job using only spare rate limit. Pull players from my tier and division via league-v4, a handful of recent ranked games each, with timelines.
- Store aggregates only: per role, distributions (median, p25, p75) of CS diff at 10, gold diff at 15, vision/min, deaths before 15, KP, damage share. No per-player records kept.
- A few thousand games is enough. Then the report and dashboard can say "below your bracket", not just "below your own average".

## Phase 5: Quartermaster

- Post-game summary to Discord after each ranked game, weekly trend digest, `/draftsmith` slash command for current form.
- Either Quartermaster calls Draftsmith's HTTP API, or Draftsmith becomes a Quartermaster module. Decide after Phase 1 is stable.

## Open decisions

- **Does Quartermaster run on the same PC as League?** If yes, `serve` and `watch` can be one process.
- ~~**Storage for the dashboard**~~: resolved 2026-11-17, SQLite (`modernc.org/sqlite`) as a rebuildable index over the raw JSON archive. See Phase 1.
- ~~**OP.GG tier filter**~~: resolved. The tier list is fixed at Emerald+ (fine for me); per-champion analysis accepts `tier` (e.g. `emerald_plus`, `master_plus`).
- **Riot product approval**: personal key registered as "Draftsmith"; if Riot asks for changes, update this file.

## Conventions

- Run `gofmt`, `go vet ./...`, `go test ./...` before every commit.
- Keep raw Riot JSON untouched in the archive; derive everything else from it.
- No secrets in the repo, logs, or fixtures.
- Commit identity: rojoloco04 <parrackjack@gmail.com>.
