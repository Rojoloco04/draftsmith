// Command draftsmith archives your ranked League of Legends games from the
// Riot API and turns them into a report and a dashboard.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/ingest"
	"github.com/rojoloco04/draftsmith/internal/opgg"
	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/store"
)

const version = "0.1.0"

const usage = `draftsmith: ranked League of Legends stats from your own match archive

Usage:
  draftsmith sync    [flags]          archive ranked games (and today's rank) from the Riot API
  draftsmith serve   [flags]          run the dashboard (and sync in the background)
  draftsmith reindex [flags]          rebuild the dashboard's SQLite index from the archive
  draftsmith report  [flags]          print a markdown report from the archive
  draftsmith export  [flags]          dump per-game features as JSON lines
  draftsmith meta tools [--schema]    list OP.GG's meta tools
  draftsmith meta call <tool> [json]  call an OP.GG tool and cache the result
  draftsmith version

Config comes from flags, environment variables, or a .env file in the
current directory:
  RIOT_API_KEY        your Riot key (required for sync)
  DRAFTSMITH_RIOT_ID    Name#TAG
  DRAFTSMITH_PLATFORM   na1, euw1, kr, ... (default na1)
  DRAFTSMITH_DATA       archive directory (default ./data)

Run "draftsmith <command> -h" for command flags.
`

func main() {
	loadDotEnv(".env")
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch os.Args[1] {
	case "sync":
		err = cmdSync(ctx, os.Args[2:])
	case "serve":
		err = cmdServe(ctx, os.Args[2:])
	case "reindex":
		err = cmdReindex(ctx, os.Args[2:])
	case "report":
		err = cmdReport(os.Args[2:])
	case "export":
		err = cmdExport(os.Args[2:])
	case "meta":
		err = cmdMeta(ctx, os.Args[2:])
	case "version":
		fmt.Println("draftsmith", version)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type common struct {
	data     string
	platform string
	riotID   string
	queues   string
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.data, "data", env("DRAFTSMITH_DATA", "data"), "archive directory")
	fs.StringVar(&c.platform, "platform", env("DRAFTSMITH_PLATFORM", "na1"), "platform: na1, euw1, kr, ...")
	fs.StringVar(&c.riotID, "riot-id", env("DRAFTSMITH_RIOT_ID", ""), "Riot ID as Name#TAG")
	fs.StringVar(&c.queues, "queues", "solo", "ranked queues: solo, flex, or both")
}

func (c *common) queueList() ([]int, error) {
	switch strings.ToLower(c.queues) {
	case "solo", "":
		return []int{riot.QueueSoloDuo}, nil
	case "flex":
		return []int{riot.QueueFlex}, nil
	case "both", "all":
		return []int{riot.QueueSoloDuo, riot.QueueFlex}, nil
	}
	return nil, fmt.Errorf("--queues must be solo, flex, or both")
}

func cmdSync(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	var c common
	c.register(fs)
	noTimelines := fs.Bool("no-timelines", false, "skip timelines (halves API calls, loses lane diffs and death data)")
	max := fs.Int("max", 0, "fetch at most N new games this run (0 = all)")
	rate := fs.String("rate", "20/1s,100/2m", "rate limits, comma separated N/duration")
	fs.Parse(args)

	sy, err := newSyncer(c, *rate)
	if err != nil {
		return err
	}
	sy.timelines = !*noTimelines
	sy.max = *max
	st, syncErr := sy.run(ctx, os.Stdout)
	if errors.Is(syncErr, context.Canceled) {
		fmt.Println("interrupted; rerun sync to continue where it stopped")
		syncErr = nil
	}
	if st.PUUID == "" {
		return syncErr
	}
	// The archive is the source of truth; keep the dashboard index current.
	if err := indexArchive(context.WithoutCancel(ctx), sy.store, st, os.Stdout); err != nil {
		return errors.Join(syncErr, fmt.Errorf("archive updated but indexing failed (run `draftsmith reindex`): %w", err))
	}
	return syncErr
}

// syncer is everything one sync run needs. sync and serve share it.
type syncer struct {
	client    *riot.Client
	store     *store.Store
	queues    []int
	riotID    string
	dataDir   string
	timelines bool
	max       int
}

func newSyncer(c common, rate string) (*syncer, error) {
	key := os.Getenv("RIOT_API_KEY")
	if key == "" {
		return nil, errors.New("RIOT_API_KEY is not set (get one at developer.riotgames.com)")
	}
	queues, err := c.queueList()
	if err != nil {
		return nil, err
	}
	limits, err := riot.ParseLimits(rate)
	if err != nil {
		return nil, err
	}
	client, err := riot.NewClient(key, c.platform, limits)
	if err != nil {
		return nil, err
	}
	if base := os.Getenv("DRAFTSMITH_RIOT_BASE_URL"); base != "" { // for tests
		client.BaseURL = func(string) string { return base }
	}
	s, err := store.Open(c.data)
	if err != nil {
		return nil, err
	}
	return &syncer{client: client, store: s, queues: queues, riotID: c.riotID, dataDir: c.data, timelines: true}, nil
}

// run resolves the account if needed, snapshots today's rank, archives new
// games, and saves state. Progress goes to out. The returned state is valid
// (with a PUUID) whenever the account is known, even if syncing failed.
func (sy *syncer) run(ctx context.Context, out io.Writer) (store.State, error) {
	s := sy.store
	st, err := s.LoadState()
	if err != nil {
		return st, err
	}

	if sy.riotID != "" && !strings.EqualFold(sy.riotID, st.RiotID) || st.PUUID == "" {
		if sy.riotID == "" {
			return st, errors.New("first sync needs --riot-id Name#TAG (or DRAFTSMITH_RIOT_ID)")
		}
		name, tag, ok := strings.Cut(sy.riotID, "#")
		if !ok {
			return st, fmt.Errorf("riot id %q should look like Name#TAG", sy.riotID)
		}
		acct, err := sy.client.AccountByRiotID(ctx, name, tag)
		if err != nil {
			return st, fmt.Errorf("looking up %s: %w", sy.riotID, err)
		}
		if st.PUUID != "" && st.PUUID != acct.PUUID {
			return st, fmt.Errorf("%s holds games for a different account (%s); use a separate --data directory", sy.dataDir, st.RiotID)
		}
		st.PUUID = acct.PUUID
		st.RiotID = acct.GameName + "#" + acct.TagLine
		st.Platform = sy.client.Platform()
		fmt.Fprintf(out, "tracking %s (%s)\n", st.RiotID, st.Platform)
	}

	if entries, err := sy.client.LeagueEntries(ctx, st.PUUID); err != nil {
		fmt.Fprintf(out, "warning: could not snapshot rank: %v\n", err)
	} else if err := s.PutRankSnapshot(time.Now(), entries); err != nil {
		return st, err
	} else {
		for _, e := range entries {
			fmt.Fprintf(out, "%s: %s %s %d LP (%dW %dL)\n", e.QueueType, e.Tier, e.Rank, e.LeaguePoints, e.Wins, e.Losses)
		}
	}

	res, syncErr := ingest.Sync(ctx, sy.client, s, &st, ingest.Options{
		Queues:    sy.queues,
		Timelines: sy.timelines,
		Max:       sy.max,
		Log:       out,
	})
	st.LastSync = time.Now()
	if err := s.SaveState(st); err != nil {
		return st, err
	}
	fmt.Fprintf(out, "archived %d matches and %d timelines (%d timelines no longer on Riot's side, %d errors)\n",
		res.NewMatches, res.NewTimelines, res.MissingTimelines, res.Errors)
	return st, syncErr
}

// indexArchive brings the SQLite index in the data dir up to date.
func indexArchive(ctx context.Context, s *store.Store, st store.State, out io.Writer) error {
	d, err := db.OpenDir(ctx, s.Dir)
	if err != nil {
		return err
	}
	defer d.Close()
	res, err := d.Index(ctx, s, st.PUUID)
	if err != nil {
		return err
	}
	if res.Added+res.Updated > 0 || d.Rebuilt {
		fmt.Fprintf(out, "indexed %d new games (%d updated)\n", res.Added, res.Updated)
	}
	return nil
}

func cmdReindex(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("reindex", flag.ExitOnError)
	data := fs.String("data", env("DRAFTSMITH_DATA", "data"), "archive directory")
	fs.Parse(args)
	s, err := store.Open(*data)
	if err != nil {
		return err
	}
	st, err := s.LoadState()
	if err != nil {
		return err
	}
	if st.PUUID == "" {
		return errors.New("no archive yet; run `draftsmith sync --riot-id Name#TAG` first")
	}
	d, err := db.OpenDir(ctx, *data)
	if err != nil {
		return err
	}
	defer d.Close()
	start := time.Now()
	res, err := d.Rebuild(ctx, s, st.PUUID)
	if err != nil {
		return err
	}
	fmt.Printf("indexed %d games and %d rank snapshots into %s in %s",
		res.Added, res.Ranks, filepath.Join(*data, db.FileName), time.Since(start).Round(time.Millisecond))
	if res.Skipped > 0 {
		fmt.Printf(" (%d archived matches without %s skipped)", res.Skipped, st.RiotID)
	}
	fmt.Println()
	return nil
}

func loadForAnalysis(c common) (*store.Store, store.State, []analysis.Game, error) {
	s, err := store.Open(c.data)
	if err != nil {
		return nil, store.State{}, nil, err
	}
	st, err := s.LoadState()
	if err != nil {
		return nil, st, nil, err
	}
	if st.PUUID == "" {
		return nil, st, nil, errors.New("no archive yet; run `draftsmith sync --riot-id Name#TAG` first")
	}
	queues, err := c.queueList()
	if err != nil {
		return nil, st, nil, err
	}
	qset := map[int]bool{}
	for _, q := range queues {
		qset[q] = true
	}
	games, err := analysis.LoadGames(s, st.PUUID, qset)
	return s, st, games, err
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	var c common
	c.register(fs)
	out := fs.String("out", "", "write to file instead of stdout")
	minGames := fs.Int("min-games", 5, "hide champions with fewer games")
	fs.Parse(args)

	s, st, games, err := loadForAnalysis(c)
	if err != nil {
		return err
	}
	in := analysis.ReportInput{RiotID: st.RiotID, Games: games, MinGames: *minGames}
	if raw, day, err := s.LatestRankSnapshot(); err == nil && raw != nil {
		var entries []riot.LeagueEntry
		if json.Unmarshal(raw, &entries) == nil {
			want := "RANKED_SOLO_5x5"
			if c.queues == "flex" {
				want = "RANKED_FLEX_SR"
			}
			for _, e := range entries {
				if e.QueueType == want {
					in.Rank = fmt.Sprintf("%s %s %d LP (%dW %dL)", e.Tier, e.Rank, e.LeaguePoints, e.Wins, e.Losses)
					in.RankDate = day
				}
			}
		}
	}

	w, closeFn, err := output(*out)
	if err != nil {
		return err
	}
	defer closeFn()
	analysis.WriteReport(w, in)
	return nil
}

func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	var c common
	c.register(fs)
	out := fs.String("out", "", "write to file instead of stdout")
	fs.Parse(args)

	_, _, games, err := loadForAnalysis(c)
	if err != nil {
		return err
	}
	w, closeFn, err := output(*out)
	if err != nil {
		return err
	}
	defer closeFn()
	enc := json.NewEncoder(w)
	for _, g := range games {
		if err := enc.Encode(g); err != nil {
			return err
		}
	}
	return nil
}

func cmdMeta(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: draftsmith meta tools [--schema] | draftsmith meta call <tool> [json-args]")
	}
	switch args[0] {
	case "tools":
		fs := flag.NewFlagSet("meta tools", flag.ExitOnError)
		schema := fs.Bool("schema", false, "print each tool's input schema")
		endpoint := fs.String("endpoint", opgg.DefaultEndpoint, "MCP endpoint")
		fs.Parse(args[1:])
		tools, err := opgg.New(*endpoint).ListTools(ctx)
		if err != nil {
			return err
		}
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		for _, t := range tools {
			fmt.Printf("%s\n  %s\n", t.Name, oneLine(t.Description))
			if *schema && len(t.InputSchema) > 0 {
				var pretty any
				if json.Unmarshal(t.InputSchema, &pretty) == nil {
					b, _ := json.MarshalIndent(pretty, "  ", "  ")
					fmt.Printf("  %s\n", b)
				}
			}
		}
		return nil

	case "call":
		fs := flag.NewFlagSet("meta call", flag.ExitOnError)
		data := fs.String("data", env("DRAFTSMITH_DATA", "data"), "archive directory (results are cached under meta/)")
		endpoint := fs.String("endpoint", opgg.DefaultEndpoint, "MCP endpoint")
		fs.Parse(args[1:])
		rest := fs.Args()
		if len(rest) == 0 {
			return errors.New("usage: draftsmith meta call <tool> [json-args]")
		}
		tool := rest[0]
		argJSON := "{}"
		if len(rest) > 1 {
			argJSON = rest[1]
		}
		var toolArgs map[string]any
		if err := json.Unmarshal([]byte(argJSON), &toolArgs); err != nil {
			return fmt.Errorf("tool arguments must be a JSON object: %w", err)
		}
		res, err := opgg.New(*endpoint).CallTool(ctx, tool, toolArgs)
		if err != nil {
			return err
		}
		fmt.Println(res.Text)
		if res.IsError {
			return fmt.Errorf("tool %s returned an error", tool)
		}
		s, err := store.Open(*data)
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(tool + argJSON))
		key := tool + "-" + hex.EncodeToString(sum[:])[:10]
		cached, _ := json.MarshalIndent(map[string]any{
			"tool": tool, "args": toolArgs, "fetchedAt": time.Now().UTC(), "result": res,
		}, "", "  ")
		if err := s.PutMeta(key, cached); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "cached to %s\n", s.MetaPath(key))
		return nil
	}
	return fmt.Errorf("unknown meta subcommand %q", args[0])
}

func output(path string) (io.Writer, func(), error) {
	if path == "" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	bw := bufio.NewWriter(f)
	return bw, func() { bw.Flush(); f.Close() }, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}

// loadDotEnv sets KEY=VALUE lines from path without overriding real env vars.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}
