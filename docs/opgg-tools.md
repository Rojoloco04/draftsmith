# OP.GG MCP tools

Recorded 2026-11-17 from `draftsmith meta tools --schema` against `https://mcp-api.op.gg/mcp`, plus probe calls. This is an agent-facing endpoint, not a documented API, so re-check when something breaks.

## Response format

Most tools do **not** return JSON. The text content is a positional "class" notation: a header declaring each class's fields in order, then one expression built from constructor calls.

```
class LolListLaneMetaChampions: position_filter,data
class Data: positions
class Positions: mid
class Mid: champion,is_rip,play,win_rate,pick_rate,role_rate,ban_rate,kda,tier,rank

LolListLaneMetaChampions("mid",Data(Positions([Mid("Ahri",false,292854,0.51,0.11,0.96,0.04,2.58,1,1),...])))
OP.GG page: https://op.gg/lol/champions/...
```

- Field order in the header follows the response, not the order you asked for in `desired_output_fields`.
- Values are strings (double quoted), numbers, `true`/`false`, lists `[...]`, and nested constructors. I haven't seen `null` or escaped quotes yet; the parser (`opgg.Parse`/`opgg.Decode`) handles both anyway.
- **Class names don't identify where a value sits.** A class is declared once and reused for every value of the same shape: the `position: all` tier list declares only `class Top` and uses `Top(...)` for the mid, jungle, adc and support lists too, and champion analysis uses `StrongCounter(...)` for `weak_counters`. Key off field names, never class names.
- Requested fields that don't exist are skipped, and the server adds a top-level `_field_diagnostics` value (`FieldDiagnostics(unmatched_fields, hint)`). Treat any unmatched field as a schema change.
- A trailing `OP.GG page: <url>` line follows the expression.
- There is no `structuredContent` in the MCP result; the text is the only payload.
- `lol_get_lane_matchup_guide` is the exception: it returns plain JSON with full precision.

**Rates are rounded to 2 decimals** in class notation (`win_rate` 0.51). That is too coarse to rank champions, so request `play` and `win` and compute `win / play` instead. Pick and ban rates have no raw counts, so 2 decimals is all there is.

Server-side validation errors come back as JSON-RPC error `-32600` with a JSON message listing allowed values.

## Common arguments

- `desired_output_fields` (required on most tools): a closed set of paths, with brace groups for several leaves, e.g. `data.positions.mid[].{champion,tier,play,win}`. Ask only for what you need; payloads shrink a lot.
- `client_app`: optional, e.g. `"Draftsmith/0.1"`.
- `lang`: defaults to `en_US`.
- Champion arguments are UPPER_SNAKE_CASE, but OP.GG normalizes by uppercasing and dropping underscores. Riot's `championName` uppercased works as is (`LEBLANC`, `CHOGATH`, `MONKEYKING`, `KAISA`, `TWISTEDFATE`).
- Positions: `top`, `mid`, `jungle`, `adc`, `support`. Riot `teamPosition` maps as TOP→top, JUNGLE→jungle, MIDDLE→mid, BOTTOM→adc, UTILITY→support.

## Tools Draftsmith uses

### `lol_list_lane_meta_champions`: tier list

Args: `position` (`all`, `top`, `mid`, `jungle`, `adc`, `support`; default `all`), `desired_output_fields`.

Fields per champion: `champion, is_rip, play, win, win_rate, pick_rate, role_rate, ban_rate, kda, kill, tier, rank, rank_prev, rank_prev_patch`. `tier` is 1 (OP) to 5 (weak).

- **No tier, region, or patch filter.** An undeclared `tier` argument is accepted and ignored. The data is Emerald+ (Yasuo mid's `play` matches `lol_get_champion_analysis` with `tier: emerald_plus` exactly), which matches the bracket I play in.
- No patch in the response. Take it from `lol_get_champion_analysis` `data.trends.win.version` (`"16.22"`) or from my latest game's patch.
- Ask for `data.positions.{top,mid,jungle,adc,support}[].{...}` to get all five lanes in one call (about 11 KB with seven fields).
- `champion` is the display name ("LeBlanc", "Twisted Fate", "Wukong", "Nunu & Willump"), not Riot's key. Join through `lol_list_champions`.

### `lol_get_champion_analysis`: builds, runes, counters for one champion

Required: `game_mode` (`ranked`, `flex`, `urf`, `aram`, `nexus_blitz`), `champion`, `position`, `desired_output_fields`. Optional: `tier`.

- **`tier` filter works**: `ALL`, `CHALLENGER`, `GRANDMASTER`, `MASTER_PLUS`, `MASTER`, `DIAMOND_PLUS`, `DIAMOND`, `EMERALD_PLUS`, `EMERALD`, `PLATINUM_PLUS`, `PLATINUM`, `GOLD_PLUS`, `GOLD`, `IBSG`, `SILVER`, `BRONZE`, `IRON` (lowercase accepted). Omitted gives the same numbers as `emerald_plus`.
- Useful fields: `data.summary.average_stats.{play,win_rate,pick_rate,ban_rate,tier,rank}`, `data.runes.{primary_page_id,primary_rune_ids[],secondary_page_id,secondary_rune_ids[],stat_mod_ids[],play,win,pick_rate}` (the IDs needed to build an LCU rune page), `data.core_items`, `data.boots`, `data.starter_items`, `data.summoner_spells`, `data.skills.order[]`, `data.strong_counters[]` / `data.weak_counters[]` `.{champion_name,play,win,win_rate,my_win_rate,counter_win_rate}`, `data.damage_type` (AD/AP/BOTH, useful for team comp gaps), `data.trends.*.version` (patch).
- In **both** counter lists, `win` counts the selected champion's wins, so `win / play` is my champion's win rate into that opponent. (The schema says `win_rate` in `weak_counters` is the counter's win rate, which is true for `win_rate` but not for `win`: Ahri into Katarina is `play` 7187, `win` 3481, `my_win_rate` 0.48.)
- `data.counters_meta` is injected when counters are empty for lack of sample (seen on Wukong mid).
- No region filter.

### `lol_get_lane_matchup_guide`: one matchup

Required: `position` (`none` infers the main lane), `my_champion`, `opponent_champion`. No `desired_output_fields`, no tier. Returns full JSON: summary stats per position, counters, spells, runes, items, and tips. Fine for the champion page and champ select, but it's large, so cache it per patch.

### `lol_list_champions`: id/key/name table

`data.champions[].{champion_id,key,name}`. `key` is Riot's `championName` (`MonkeyKing`, `Leblanc`, `TwistedFate`), `name` is the display name the tier list uses. Fetch once per patch.

The table also lists mode variants with ids from 60001 and keys like `Jade_Annie` that share display names with the real champions (84 of 257 rows on 2026-11-17). Skip any key containing `_`; Riot keys never have one.

### `lol_get_summoner_profile`

Required: `game_name`, `tag_line`, `region` (e.g. `NA`), `desired_output_fields`. Includes `data.summoner.lp_histories[]` (LP over time) and per-champion season stats. The Riot API already gives me everything I need for my own account, so this is optional: at most an LP history backfill for days before Draftsmith started taking snapshots. Never call it for anyone else.

## Not used

`lol_list_summoner_matches` and `lol_get_summoner_game_detail` duplicate the Riot API. `lol_list_champion_leaderboard` and `lol_get_pro_player_riot_id` look up other players, which is out of scope. Also skipped: the esports, skins, ARAM, TFT, and Valorant tools.
