# Draftsmith

Personal ranked League of Legends stats from your own match archive. It pulls your ranked games from the Riot API, keeps the raw JSON locally so nothing is lost when Riot ages it out, and builds a report and a local dashboard on your roles, champion pool, laning, early deaths, and how you convert leads.

Pure Go, no cgo. Dependencies: `modernc.org/sqlite` for the dashboard's index, and `go-webview2` with `golang.org/x/sys` for the app and overlay windows on Windows.

## Quick start (Windows, no Go needed)

1. Download `draftsmith.exe` from the [latest release](https://github.com/rojoloco04/draftsmith/releases/latest) and put it in a folder of its own (it keeps its settings and your archive next to itself).
2. Double-click it. Windows SmartScreen may warn about an unrecognized app the first time: **More info**, then **Run anyway**.
3. Draftsmith opens on its Settings page. Paste a Riot API key (sign in at [developer.riotgames.com](https://developer.riotgames.com) and copy the development key), then add your Riot ID. Your ranked history starts syncing right away and the dashboard fills in when it's done.

A development key expires every 24 hours. When it does, every page says so; paste a fresh one in Settings. Register a personal app on the same site for a key that lasts.

## Setup from source

1. Get a key at [developer.riotgames.com](https://developer.riotgames.com). A dev key works right away but expires every 24 hours. Register a personal app for a key that doesn't expire (same rate limits: 20 req/s, 100 req/2 min).
2. Copy `.env.example` to `.env` and fill it in, set the same variables in your shell, or start the app and use its Settings page.
3. Build:

```sh
go build -o draftsmith ./cmd/draftsmith     # draftsmith.exe on Windows
```

## Usage

```sh
draftsmith sync --riot-id "Name#TAG"      # first run; later runs just `draftsmith sync`
draftsmith report                          # markdown report to stdout
draftsmith report --out report.md
draftsmith export --out games.jsonl        # per-game features, one JSON object per line
```

`sync` is incremental and safe to interrupt. The first run pages through every ranked game Riot still has, which on a personal key is roughly 1.2 seconds per API call (one call per match, one per timeline). Ctrl-C stops it cleanly and the next run picks up where it left off. Each run also snapshots your current rank, since Riot only exposes the current split's standing.

Flags worth knowing:

- `--queues solo|flex|both` (default solo)
- `--no-timelines` halves the API calls but drops lane diffs and death data
- `--max N` caps how many new games one run fetches
- `--platform euw1` etc. if you're not on NA

Several accounts can share one archive: run `draftsmith sync --riot-id "Name#TAG"` once per account to add it. After that, `sync` and `serve` keep every tracked account up to date, and the dashboard can switch between them or show them together.

### Dashboard

Run `draftsmith` with no arguments (or double-click `draftsmith.exe`) on the PC you play on and Draftsmith opens as a desktop app: the dashboard in its own window, background sync, the live champ select page, the overlay beside the League client, and the in-game build. Closing the window quits it. The dashboard is still served on http://127.0.0.1:8080 underneath, so a browser tab works too, and a second double-click while it runs just opens another window. It finds `.env` and `data/` next to the exe if they aren't in the current folder. It runs in the background with no console window, whether double-clicked or started from a terminal, and logs to `data/draftsmith.log`; `draftsmith --debug` keeps it in the terminal with the log there. The window's title bar takes the theme's header colors (Windows 11; Windows 10 gets a dark or light title bar). If the WebView2 runtime is missing (it ships with Windows 10 and 11), it opens the dashboard in your browser instead. If it can't start at all, it says why in a message box.

When the exe sits in its source checkout and Go is on PATH, it first builds the source to a scratch file (about a second when nothing changed) and, only if that differs from the running exe, swaps it in and starts the new build; the old copy is set aside as `draftsmith.exe~` (Windows can't overwrite a running program) and deleted once it exits. A compile error is logged and the existing build runs anyway. For a desktop shortcut, point it at `draftsmith.exe` and use `cmd/draftsmith/winres/icon.ico` (the dashboard's logo) as its icon; the app window uses the same logo.

The **Settings** page (the gear in the header) holds the Riot API key (checked with Riot before it's saved, and only its last four characters shown after), the region, which ranked queues to sync, the theme (match Windows, dark or light), whether to show the overlay, and the tracked accounts: add one by Riot ID or remove one (its games stay on disk, so adding it back doesn't refetch them). It writes them to `.env`. Settings can only be changed from the PC Draftsmith runs on, not over Tailscale. With no account yet, the archive pages send you there, and while an account's first games sync they show a progress page instead of an empty dashboard.

`draftsmith serve` is the same without the app window (`--app` adds it, `--open` opens the browser instead), and takes flags to turn things off:

```sh
draftsmith serve --addr 100.x.y.z:8080     # bind your Tailscale IP to open it from your phone
draftsmith serve --no-sync                 # just browse the archive
draftsmith serve --no-meta                 # don't call OP.GG
draftsmith serve --no-watch                # don't follow the League client (e.g. on another PC)
draftsmith serve --no-overlay              # champ select page only, no window beside the client
draftsmith reindex                         # rebuild data/draftsmith.db from the archive
```

Pages: an overview (rank banner with headline numbers, rank history, your last 20 games, and your champion pool plotted by games and adjusted win rate), match history with role and champion filters, a match page (both teams' stats with a DS score for every player, your gold and CS against your lane opponent, where you died), per-champion stats with matchups and your roles, and an OP.GG tier list.

The **DS score** rates every player in a match from 0 to 10 against the other nine: KDA, kill participation, damage share, gold per minute, gold lead over the lane opponent, CS per minute, vision per minute and deaths per minute, each compared with the lobby and weighted by role (supports aren't judged on CS), plus a small bump for the winning team. The best player on the winning team is the **MVP** and the best on the losing team the **SVP**. Match history shows yours next to the champion; the match page shows all ten.

Background sync checks for new games every 25 minutes. To pick up a game you just finished, press **Refresh** in the header: it shows a count of the games it's fetching, syncs right away and reloads the page when it's done.

OP.GG data (Emerald+, current patch) shows up in three places: the tier list page, with your own record next to each champion; a "your pool vs the meta" card and a meta tier column on the champions page; and an OP.GG card on each champion page with its tier, rates, and best and worst matchups, next to your record against those champions. Responses are cached in `data/draftsmith.db` and refreshed daily. If OP.GG is unreachable, the last copy is shown with its age.

### Performance review with Claude Code

The repo ships a Claude Code skill, `.claude/skills/performance-review`. Open the repo in Claude Code and ask for a review ("how have my last 30 games gone?", "why do I keep losing on Vladimir?"). It reads `data/draftsmith.db` read-only through `review_data.py` (results, roles, champions, laning, DS score, deaths by game phase and map zone, matchups), digs into whatever stands out, and writes a short review with what's working, what's costing games, and one thing to focus on next. You can run the script yourself too: `python .claude/skills/performance-review/review_data.py --last 30`.

### Champ select

`draftsmith watch` follows the League client on the same PC (it reads the client's local API, which only listens on localhost) and, in champ select, logs the draft and suggestions from your pool. `serve` does the same inside the dashboard: the **Champ select** page updates live over server-sent events with both teams, bans, each enemy's likely lane, your top three picks (estimated win rate from your record, OP.GG's win rate and the likely lane matchup) and OP.GG's rune page for each, or just your champion's once you lock in.

Your own hover always gets a card, with its runes fetched ahead of everything else. Suggestions also weigh your team's comp: a pick that brings magic damage to a mostly AD team (or the reverse), or the team's first tank, scores a little higher, and the board shows your team's damage mix and frontline. Damage type and frontline come from Riot's champion classes in Data Dragon.

**Import to client** under a rune page puts it in the League client as "Draftsmith Imported" and selects it. Draftsmith keeps just that one page and updates it on every import; it never touches your own pages. The first import needs a free page slot; if every slot is full, delete one or rename one to start with "Draftsmith" and it'll be reused.

During the game the page shows your champion's whole OP.GG build: starting items, core paths, boots, options for the 4th to 6th items, other late items, and the skill order. It reads your level and items from the game's Live Client Data API (your champion only), ticks off what you own, and points at the next item and the next skill to level.

Apart from the Import button it only reads. It never hovers, picks or bans, and it shows champions and lanes but never anyone's name.

The dashboard has no login, so it only listens where you tell it to and refuses `0.0.0.0`. Keep it on localhost or Tailscale; don't port forward it. Other players appear as champion and position only. Champion, item, rune and summoner spell icons and splash art load from Riot's Data Dragon CDN, and ranked emblems from CommunityDragon (`raw.communitydragon.org`); those URLs carry only ids and tier names. Rune and spell names are embedded from Data Dragon (`internal/web/ddragon`).

`data/draftsmith.db` is a SQLite index built from the raw archive. `sync` and `serve` keep it current, and it's safe to delete: `reindex` (or the next `serve`) rebuilds it.

### Why sync early and often

Riot keeps matches for about two years and timelines for about one, on a rolling basis. Anything archived here stays forever, so run `sync` regularly (a weekly cron or Task Scheduler job is plenty).

### Meta data from OP.GG

OP.GG runs a public MCP server with champion tiers, win/pick/ban rates, counters, and builds. `draftsmith` talks to it directly over HTTP, no AI client needed.

```sh
draftsmith meta tools --schema             # see what's available and each tool's arguments
draftsmith meta call lol_list_lane_meta_champions '{...args from the schema...}'
```

`meta call` prints the raw response; redirect it to a file to keep it. The tool arguments aren't documented in OP.GG's README, so start with `meta tools --schema` to see them. Most tools answer in a positional "class" notation rather than JSON; `docs/opgg-tools.md` describes it and the quirks found so far.

## What the report covers

- Win rate, KDA, CS/min, and vision by season (calendar year) and by role
- Kill participation, damage share, control wards, CS diff at 10, gold diff at 15, and deaths before 15 by role
- Champion pool with an adjusted win rate: each champion's record is blended with 10 games at your overall win rate, so a 7-2 champion doesn't look like your best pick
- Your pool vs the meta: each champion with 3+ games next to its OP.GG tier in the role you play it most. The report reads only the OP.GG cache that `serve` fills and never calls OP.GG itself; the section is left out until something is cached
- Win rate bucketed by gold lead at 15 (shows whether you lose lanes or throw leads)
- Toughest lane matchups in your main role
- Last 20 games vs everything before
- Observations drawn from the above (thrown leads, early deaths in losses, deaths on the enemy half of the map, champions to keep or bench)

Everything compares you to yourself right now.

## Layout

```
cmd/draftsmith        CLI
internal/riot       Riot API client: multi-window rate limiter, 429 retry, account/match/league endpoints
internal/store      gzipped raw JSON archive under data/
internal/ingest     incremental sync with resumable backfill
internal/analysis   per-game features, the shared summary, and the markdown report
internal/db         SQLite index over the archive for the dashboard
internal/web        dashboard handlers, templates, SVG charts, static files (embedded)
internal/opgg       MCP client for OP.GG, and the parser for its class notation
internal/meta       typed, cached OP.GG calls (tier list, champion analysis) and the pool-vs-meta join
internal/lcu        League client's local API: discovery, champ select, rune page import
internal/watch      follows the client's gameflow and champ select
internal/draft      enemy lane guesses, team comp, and pick suggestions from your pool
internal/liveclient in-game Live Client Data API (your champion's level and items)
internal/overlay    WebView2 window docked beside the League client (Windows)
internal/appwin     the desktop app's own window around the dashboard (Windows)
internal/fixture    synthetic match/timeline JSON (and one scrubbed real game) for tests
```

## Roadmap

- `sample`: crawl games from players in your tier to build real benchmarks (CS diff, vision, early deaths) for your role and elo, so the report can say "below your bracket" instead of only "below your own average"
- Quartermaster integration: post-game report in Discord

## Riot policy

Personal keys are for you and a small private group. Don't host this publicly without a production key. Shared with friends, each person runs their own copy with their own key; don't hand out yours.
