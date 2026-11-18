# Draftsmith plan

A personal OP.GG/U.GG for one player: my ranked stats dashboard, a meta tier list, and a champ select helper. This file is the source of truth for scope and decisions; update it as things change.

## Where things stand

Done (commits `09ccc8e`, `b544eaf`):

- `internal/riot`: Riot API client with a multi-window rate limiter (20/1s, 100/2m by default), 429 Retry-After handling, `ErrNotFound`/`ErrAuth` sentinels. Endpoints: account-v1 by Riot ID, match-v5 ids/match/timeline, league-v4 entries by PUUID.
- `internal/store`: raw match and timeline JSON archived gzipped under `data/`, plus state, daily rank snapshots, and an OP.GG response cache.
- `internal/ingest`: incremental sync. Pages the full match id list until a backfill completes (and every listed game is stored), then stops at the first fully archived page. Timelines Riot no longer has get a `.missing` marker.
- `internal/analysis`: per-game features (CS/gold/vision per min, KP, damage share, gold/CS/XP diff vs lane opponent at 10 and 15, deaths before 15, deaths on the enemy half, death positions) and a markdown report.
- `internal/opgg`: MCP client (Streamable HTTP, JSON or SSE responses, session header) for OP.GG's public server at `https://mcp-api.op.gg/mcp`.
- CLI: `sync`, `report`, `export`, `meta tools`, `meta call`, `version`.
- Tests use synthetic fixtures (`internal/fixture`) and httptest fakes. Nothing has been run against the real Riot API or OP.GG yet.

Why stdlib only so far: the sandbox it was written in couldn't reach the Go module proxy. That constraint is gone on this machine, so dependencies are fine where they earn their place (see Open decisions).

## Phase 0: verify against real data (do first)

1. Copy `.env.example` to `.env`, set `RIOT_API_KEY` and `DRAFTSMITH_RIOT_ID`. `.env` is gitignored, keep it that way.
2. `go build -o draftsmith.exe ./cmd/draftsmith`, then `draftsmith sync --max 20` as a smoke test, then a full `draftsmith sync`.
3. Check the things the fixtures assumed:
   - `league-v4 /entries/by-puuid/{puuid}` returns the shape in `riot.LeagueEntry`.
   - Match `teamPosition` values and timeline `participantFrames` / `CHAMPION_KILL` events match `riot/types.go`.
   - `gameVersion` parses to a sensible patch, `gameDuration` is seconds.
4. `draftsmith report` and sanity check the numbers against OP.GG for a few games.
5. `draftsmith meta tools --schema` and record the input schemas for `lol_list_lane_meta_champions`, `lol_get_champion_analysis`, `lol_get_lane_matchup_guide`, `lol_get_summoner_profile` in `docs/opgg-tools.md`. Key question: is there a tier filter (Emerald+, Master+), and a position/region/patch filter?
6. Save one real match + timeline (my own games only) as fixtures under `internal/fixture/testdata/` and add a test that parses them. Scrub other players' PUUIDs and Riot IDs before committing.

## Phase 1: dashboard (`draftsmith serve`)

Goal: an OP.GG-style site for me, served by the Go binary, reachable over Tailscale only.

- Server-rendered `html/template` + `embed`, htmx for interactivity, a small chart library vendored into `web/static` (no CDN). No SPA build step.
- Bind to `127.0.0.1` by default, `--addr` to bind the Tailscale IP. No auth beyond that, so never bind `0.0.0.0` and never port forward.
- Background jobs inside `serve`: sync my games every 20 to 30 minutes, OP.GG meta refresh daily. My own sync gets rate limit priority over everything else.
- Pages:
  - **Overview**: rank + LP history (from daily snapshots), win rate trend, role split, top champions, "what stands out" notes from the report.
  - **Match history**: list with champion, KDA, CS, result, lane diffs; detail view with both teams' end-of-game stats and my gold/CS curve vs my lane opponent.
  - **Champions**: my per-champion table (games, raw and adjusted win rate, KDA, CS/min, lane diffs) with drill-down to matchups.
  - **Tier list**: from OP.GG, filter by role and (if supported) Emerald+ / Master+. Show only champions above a pick rate floor.
  - **Champion page**: OP.GG build, runes, counters, alongside my own record on that champion.
  - **Champ select**: live view fed by `watch` (Phase 3), via SSE.
- Analysis should move from "load every gzip on each request" to a cached feature table that updates on sync (in memory with a snapshot file, or SQLite; see Open decisions).

## Phase 2: OP.GG meta integration

- `internal/meta`: typed wrappers over the OP.GG tools once Phase 0 shows the schemas. Cache each response with a timestamp and patch; serve stale data if OP.GG is down and show its age.
- Tier list: one call per role (per bracket if filterable) per day.
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
- **Storage for the dashboard**: stay with files + in-memory cache, or add `modernc.org/sqlite` (pure Go, no cgo) now that dependencies are available. Leaning SQLite once Phase 1 needs filtering and pagination over match history.
- **OP.GG tier filter**: if the tools can't filter by Emerald+/Master+, accept OP.GG's default bracket or reconsider a small crawler for those brackets.
- **Riot product approval**: personal key registered as "Draftsmith"; if Riot asks for changes, update this file.

## Conventions

- Run `gofmt`, `go vet ./...`, `go test ./...` before every commit.
- Keep raw Riot JSON untouched in the archive; derive everything else from it.
- No secrets in the repo, logs, or fixtures.
- Commit identity: rojoloco04 <parrackjack@gmail.com>.
