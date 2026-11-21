---
name: performance-review
description: Review my League of Legends ranked play from the Draftsmith archive and tell me what's going well, what to fix, and what to focus on next. Use when I ask for a performance review, how I've been playing, what I should improve, why I'm losing, where or when I die, how a champion, role or matchup is going for me, or to look over a recent game or stretch of games.
---

# Performance review

You're reviewing one player's ranked games, the owner of this repo, using the data Draftsmith already archived. Be a direct, specific coach: every point needs numbers behind it, a sample size, and something concrete to do about it.

## 1. Scope

Work out from the request what to cover, defaulting to the last 30 solo queue games across all my accounts. Narrow it if I named a champion, a role, a date or a number of games. Don't ask unless the request is ambiguous in a way that changes the answer.

## 2. Pull the numbers

Run the helper from the repo root. It reads `data/draftsmith.db` read-only and prints markdown tables:

```sh
python .claude/skills/performance-review/review_data.py --last 30
python .claude/skills/performance-review/review_data.py --queue solo --since 2026-11-01
python .claude/skills/performance-review/review_data.py --champion Vladimir --role MIDDLE
```

Flags: `--queue solo|flex|all`, `--last N`, `--since YYYY-MM-DD`, `--champion` (Riot id, e.g. `MonkeyKing`, `Kaisa`), `--role` (`TOP`, `JUNGLE`, `MIDDLE`, `BOTTOM`, `UTILITY`), `--data DIR`.

It covers: overall numbers (with last 15 vs before, and wins vs losses), roles, champions with adjusted win rate, gold diff at 15 against results, laning diffs at 10, OP score and place distribution, deaths by game phase and named map zone (red side games turned around so my base is always bottom left), and lane matchups.

If the index doesn't exist, the dashboard hasn't run yet: say so and stop. Don't run `draftsmith sync` (it calls the Riot API) unless I ask.

## 3. Dig where it's interesting

The helper is the starting point, not the whole review. When something stands out, query the index directly, read-only:

```python
import sqlite3
db = sqlite3.connect("file:data/draftsmith.db?mode=ro", uri=True)
```

Tables (see `internal/db/db.go` for every column):

- `games`: one row per game of mine. `win`, `champion`, `role`, `opponent` (lane opponent's champion), `minutes`, `kills`/`deaths`/`assists`, `cs_per_min`, `kill_participation`, `damage_share`, `vision_per_min`, `control_wards`, `at10_*`/`at15_*` (gold, cs, xp and their `_diff` vs the lane opponent, valid when `_has_opp` is 1), `deaths_before_15`, `deaths_enemy_side`, `queue` (420 solo, 440 flex), `remake`, `start_ms`. `account` is a PUUID: never print it.
- `participants`: all ten players' end-of-game lines, champion and position only (`me` = 1 is me). `op_score` (0-10), `op_rank` (1-10), `award` (`MVP` best on the winning team, `SVP` best on the losing team).
- `deaths`: my deaths with `minute`, `x`, `y` (0-14870; blue base bottom left; my side is `participants.team_id` 100 blue, 200 red).
- `frames`: my and my lane opponent's gold, CS and XP each minute.
- `rank_snapshots`: one row per account per day (`tier`, `division`, `lp`, `wins`, `losses`).

Good follow-ups: which champions or matchups drive a weak stat; whether deaths in a zone cluster in losses or in one phase; whether leads are thrown (ahead at 15 and still lost: look at deaths after 15 and where); whether a recent change (a new champion, a role swap) moved the numbers; one game in detail from `frames`, `deaths` and `participants` when I name it.

`draftsmith report` (markdown, `--account all`) and `draftsmith export --out <file>` (per-game JSON lines) also exist; write anything they produce to the scratchpad, not the repo.

## 4. Write the review

In the chat, not a file, unless I ask for one:

1. **Headline**: one or two sentences on where things stand.
2. **Working**: two or three strengths, each with the numbers.
3. **Costing games**: the two or three things that matter most, each with evidence (numbers, sample sizes, how wins and losses differ) and a concrete, practical fix. A death zone becomes "you die in top river 17 times, 59% in losses, mostly before 14: ward it before shoving", not "die less".
4. **Next session**: one focus to try, specific enough to check in the next review.

## Ground rules

- Small samples: under 5 games for a champion or matchup is anecdote, so say so. Lean on adjusted win rate, (wins + 10 x overall) / (games + 10), the dashboard's measure.
- Compare me with myself (wins vs losses, recent vs before, role vs role). There's no elo benchmark data, so don't claim what's normal for my rank as if it were measured.
- OP score is Draftsmith's own rating against the lobby (`internal/analysis/opscore.go`), not OP.GG's. Use it for "how much did I matter", not as truth.
- Read only. Never write to `data/` or change the archive.
- Other players are champions and positions, nothing more. Don't look anyone up.
