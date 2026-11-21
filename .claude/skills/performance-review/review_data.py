"""Pull the numbers for a Draftsmith performance review.

Reads the dashboard's SQLite index (data/draftsmith.db) read-only and
prints markdown tables: results, roles, champions, laning, OP score,
deaths by game phase and map zone, and lane matchups. Prints no PUUIDs
or Riot IDs (the index holds none for other players anyway).

    python .claude/skills/performance-review/review_data.py [--queue solo|flex|all]
        [--last N] [--since YYYY-MM-DD] [--champion Name] [--role TOP|JUNGLE|MIDDLE|BOTTOM|UTILITY]
        [--data DIR]
"""

import argparse
import math
import os
import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timezone

QUEUES = {"solo": 420, "flex": 440, "all": None}
PRIOR = 10  # adjusted win rate: (wins + PRIOR * base) / (games + PRIOR)
EDGE = 14870.0  # Summoner's Rift's far corner; blue base bottom left
PHASES = [("Laning", 0, 14), ("Mid game", 14, 25), ("Late game", 25, 1e9)]


def zone(x, y):
    """Names a position seen from my side (red side games turned around)."""
    s = x + y
    if x < 3600 and y < 3600:
        return "Own base"
    if x > EDGE - 3600 and y > EDGE - 3600:
        return "Enemy base"
    if (x < 2300 and y >= 3600) or (y > EDGE - 2300 and x <= EDGE - 3600):
        return "Top lane, their side" if y > EDGE - 2300 and x > 4500 else "Top lane"
    if (y < 2300 and x >= 3600) or (x > EDGE - 2300 and y <= EDGE - 3600):
        return "Bot lane, their side" if x > EDGE - 2300 and y > 4500 else "Bot lane"
    if abs(x - y) < 1300:
        return "Mid lane, their side" if s > EDGE else "Mid lane"
    if abs(s - EDGE) < 1500:
        return "Top river" if x < y else "Bot river"
    if s < EDGE:
        return "Own jungle, top side" if x < y else "Own jungle, bot side"
    return "Enemy jungle, top side" if x < y else "Enemy jungle, bot side"


def pct(x):
    return "-" if x is None or (isinstance(x, float) and math.isnan(x)) else f"{x * 100:.0f}%"


def num(x, d=1):
    return "-" if x is None or (isinstance(x, float) and math.isnan(x)) else f"{x:+.{d}f}" if d == 0 else f"{x:.{d}f}"


def avg(xs):
    xs = [x for x in xs if x is not None]
    return sum(xs) / len(xs) if xs else float("nan")


def table(head, rows):
    out = ["| " + " | ".join(head) + " |", "|" + "---|" * len(head)]
    out += ["| " + " | ".join(str(c) for c in r) + " |" for r in rows]
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", default="data")
    ap.add_argument("--queue", default="solo", choices=QUEUES)
    ap.add_argument("--last", type=int, default=0, help="only the N most recent games")
    ap.add_argument("--since", help="only games on or after this date (YYYY-MM-DD)")
    ap.add_argument("--champion", help="Riot champion id, e.g. MonkeyKing")
    ap.add_argument("--role", help="Riot position, e.g. MIDDLE")
    a = ap.parse_args()

    path = os.path.join(a.data, "draftsmith.db")
    if not os.path.exists(path):
        sys.exit(f"no index at {path}: run draftsmith (serve or sync) first")
    db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    db.row_factory = sqlite3.Row

    where, args = ["g.remake = 0"], []
    if QUEUES[a.queue]:
        where.append("g.queue = ?")
        args.append(QUEUES[a.queue])
    if a.since:
        ms = int(datetime.strptime(a.since, "%Y-%m-%d").replace(tzinfo=timezone.utc).timestamp() * 1000)
        where.append("g.start_ms >= ?")
        args.append(ms)
    if a.champion:
        where.append("g.champion = ?")
        args.append(a.champion)
    if a.role:
        where.append("g.role = ?")
        args.append(a.role.upper())
    games = db.execute(
        f"""SELECT g.*, p.team_id, p.op_score, p.op_rank, p.award
            FROM games g LEFT JOIN participants p ON p.match_id = g.match_id AND p.me = 1
            WHERE {' AND '.join(where)} ORDER BY g.start_ms""", args).fetchall()
    if a.last:
        games = games[-a.last:]
    if not games:
        sys.exit("no games match")
    ids = {g["match_id"] for g in games}
    by_id = {g["match_id"]: g for g in games}

    n = len(games)
    wins = sum(g["win"] for g in games)
    base = wins / n
    first = datetime.fromtimestamp(games[0]["start_ms"] / 1000).date()
    last = datetime.fromtimestamp(games[-1]["start_ms"] / 1000).date()
    print(f"# Draftsmith review data\n\n{n} games ({a.queue} queue{', ' + a.champion if a.champion else ''}"
          f"{', ' + a.role if a.role else ''}), {first} to {last}. Remakes left out.\n")

    def summary(gs):
        k = sum(g["kills"] for g in gs)
        d = sum(g["deaths"] for g in gs)
        as_ = sum(g["assists"] for g in gs)
        tl = [g for g in gs if g["has_timeline"]]
        return [len(gs), f"{sum(g['win'] for g in gs)}-{len(gs) - sum(g['win'] for g in gs)}",
                pct(sum(g["win"] for g in gs) / len(gs)), f"{(k + as_) / max(1, d):.2f}",
                num(avg([g["cs_per_min"] for g in gs])), pct(avg([g["kill_participation"] for g in gs])),
                pct(avg([g["damage_share"] for g in gs])), num(avg([g["vision_per_min"] for g in gs]), 2),
                num(avg([g["deaths_before_15"] for g in tl])), num(avg([g["deaths_enemy_side"] for g in tl])),
                num(avg([g["op_score"] for g in gs if g["op_rank"]]))]

    head = ["Games", "W-L", "WR", "KDA", "CS/m", "KP", "Dmg share", "Vision/m", "Deaths <15", "Deaths enemy half", "OP"]
    print("## Overall\n")
    rows = [["All"] + summary(games)]
    if n >= 30:
        rows.append(["Last 15"] + summary(games[-15:]))
        rows.append(["Before"] + summary(games[:-15]))
    rows.append(["Wins"] + summary([g for g in games if g["win"]]) if wins else ["Wins"] + ["-"] * len(head))
    rows.append(["Losses"] + summary([g for g in games if not g["win"]]) if wins < n else ["Losses"] + ["-"] * len(head))
    print(table([""] + head, rows) + "\n")

    def groups(key, min_games=1):
        gs = defaultdict(list)
        for g in games:
            gs[key(g)].append(g)
        out = []
        for k, v in gs.items():
            if len(v) < min_games:
                continue
            w = sum(g["win"] for g in v)
            out.append((k, v, (w + PRIOR * base) / (len(v) + PRIOR)))
        return sorted(out, key=lambda r: (-len(r[1]), r[0]))

    print("## Roles\n")
    print(table(["Role", "Games", "W-L", "WR", "Adj. WR", "OP", "Gold @15 diff", "CS @10 diff"],
                [[k or "?", len(v), f"{sum(g['win'] for g in v)}-{len(v) - sum(g['win'] for g in v)}",
                  pct(sum(g["win"] for g in v) / len(v)), pct(adj), num(avg([g["op_score"] for g in v if g["op_rank"]])),
                  num(avg([g["at15_gold_diff"] for g in v if g["at15_has_opp"]]), 0),
                  num(avg([g["at10_cs_diff"] for g in v if g["at10_has_opp"]]))] for k, v, adj in groups(lambda g: g["role"])]) + "\n")

    print(f"## Champions\n\nAdjusted WR = (wins + {PRIOR} x {pct(base)}) / (games + {PRIOR}).\n")
    print(table(["Champion", "Games", "W-L", "Adj. WR", "KDA", "CS/m", "OP", "Gold @15 diff", "Last played"],
                [[k, len(v), f"{sum(g['win'] for g in v)}-{len(v) - sum(g['win'] for g in v)}", pct(adj),
                  f"{(sum(g['kills'] + g['assists'] for g in v)) / max(1, sum(g['deaths'] for g in v)):.2f}",
                  num(avg([g["cs_per_min"] for g in v])), num(avg([g["op_score"] for g in v if g["op_rank"]])),
                  num(avg([g["at15_gold_diff"] for g in v if g["at15_has_opp"]]), 0),
                  datetime.fromtimestamp(v[-1]["start_ms"] / 1000).date()] for k, v, adj in groups(lambda g: g["champion"])]) + "\n")

    print("## Laning and conversion\n")
    buckets = [("behind 1000+", -1e9, -1000), ("behind 300-1000", -1000, -300), ("even (within 300)", -300, 301),
               ("ahead 300-1000", 301, 1001), ("ahead 1000+", 1001, 1e9)]
    rows = []
    for label, lo, hi in buckets:
        v = [g for g in games if g["at15_has_opp"] and lo <= g["at15_gold_diff"] < hi]
        rows.append([label, len(v), pct(sum(g["win"] for g in v) / len(v)) if v else "-"])
    print(table(["Gold diff @15", "Games", "WR"], rows) + "\n")
    lane = [g for g in games if g["at10_has_opp"]]
    print(f"Average @10: gold {num(avg([g['at10_gold_diff'] for g in lane]), 0)}, CS {num(avg([g['at10_cs_diff'] for g in lane]))}, "
          f"XP {num(avg([g['at10_xp_diff'] for g in lane]), 0)} over {len(lane)} games with a lane opponent.\n")

    print("## OP score (0-10 against the lobby; MVP best winner, SVP best loser)\n")
    scored = [g for g in games if g["op_rank"]]
    if scored:
        places = defaultdict(int)
        for g in scored:
            places[g["op_rank"]] += 1
        print(f"Average {num(avg([g['op_score'] for g in scored]))}, average place {num(avg([g['op_rank'] for g in scored]))}, "
              f"MVP {sum(g['award'] == 'MVP' for g in scored)}, SVP {sum(g['award'] == 'SVP' for g in scored)} "
              f"of {len(scored)} games.\n")
        print(table(["Place"] + [str(i) for i in range(1, 11)], [["Games"] + [places[i] for i in range(1, 11)]]) + "\n")

    print("## Deaths by phase and zone (my side: red side games turned around)\n")
    deaths = [d for d in db.execute("SELECT match_id, minute, x, y FROM deaths").fetchall() if d["match_id"] in ids]
    if deaths:
        zones = defaultdict(lambda: [0, 0, 0.0])  # deaths, in losses, minute sum
        cell = defaultdict(int)
        for d in deaths:
            g = by_id[d["match_id"]]
            x, y = float(d["x"]), float(d["y"])
            if g["team_id"] == 200:
                x, y = EDGE - x, EDGE - y
            z = zone(x, y)
            zones[z][0] += 1
            zones[z][1] += 0 if g["win"] else 1
            zones[z][2] += d["minute"]
            for name, lo, hi in PHASES:
                if lo <= d["minute"] < hi:
                    cell[(z, name)] += 1
        tl = sum(1 for g in games if g["has_timeline"])
        print(f"{len(deaths)} deaths over {tl} games with a timeline.\n")
        order = sorted(zones, key=lambda z: -zones[z][0])
        print(table(["Zone", "Deaths", "In losses", "Avg minute"] + [p[0] for p in PHASES],
                    [[z, zones[z][0], pct(zones[z][1] / zones[z][0]), f"{zones[z][2] / zones[z][0]:.1f}"] +
                     [cell[(z, p[0])] for p in PHASES] for z in order]) + "\n")

    print("## Lane matchups (2+ games)\n")
    mu = [(k, v, adj) for k, v, adj in groups(lambda g: g["opponent"], 2) if k]
    print(table(["Opponent", "Games", "W-L", "Adj. WR", "Gold @15 diff"],
                [[k, len(v), f"{sum(g['win'] for g in v)}-{len(v) - sum(g['win'] for g in v)}", pct(adj),
                  num(avg([g["at15_gold_diff"] for g in v if g["at15_has_opp"]]), 0)]
                 for k, v, adj in sorted(mu, key=lambda r: r[2])]) + "\n" if mu else "None yet.\n")


if __name__ == "__main__":
    main()
