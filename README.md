# Draftsmith

Personal ranked League of Legends stats from your own match archive. It pulls your ranked games from the Riot API, keeps the raw JSON locally so nothing is lost when Riot ages it out, and builds a report on your roles, champion pool, laning, early deaths, and how you convert leads.

Standard library only, no dependencies.

## Setup

1. Get a key at [developer.riotgames.com](https://developer.riotgames.com). A dev key works right away but expires every 24 hours. Register a personal app for a key that doesn't expire (same rate limits: 20 req/s, 100 req/2 min).
2. Copy `.env.example` to `.env` and fill it in, or set the same variables in your shell.
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

### Why sync early and often

Riot keeps matches for about two years and timelines for about one, on a rolling basis. Anything archived here stays forever, so run `sync` regularly (a weekly cron or Task Scheduler job is plenty).

### Meta data from OP.GG

OP.GG runs a public MCP server with champion tiers, win/pick/ban rates, counters, and builds. `draftsmith` talks to it directly over HTTP, no AI client needed.

```sh
draftsmith meta tools --schema             # see what's available and each tool's arguments
draftsmith meta call lol_list_lane_meta_champions '{...args from the schema...}'
```

Results are cached under `data/meta/`. The tool arguments aren't documented in OP.GG's README, so start with `meta tools --schema` to see them.

## What the report covers

- Win rate, KDA, CS/min, and vision by season (calendar year) and by role
- Kill participation, damage share, control wards, CS diff at 10, gold diff at 15, and deaths before 15 by role
- Champion pool with an adjusted win rate: each champion's record is blended with 10 games at your overall win rate, so a 7-2 champion doesn't look like your best pick
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
internal/analysis   per-game features and the report
internal/opgg       MCP client for OP.GG meta data
internal/fixture    synthetic match/timeline JSON for tests
```

## Roadmap

- `sample`: crawl games from players in your tier to build real benchmarks (CS diff, vision, early deaths) for your role and elo, so the report can say "below your bracket" instead of only "below your own average"
- Join OP.GG meta into the champion pool section (your adjusted win rate vs the champion's tier this patch)
- Death position heatmap
- LLM writeup over `export` output
- Quartermaster integration: post-game report in Discord

## Riot policy

Personal keys are for you and a small private group. Don't host this publicly without a production key.
