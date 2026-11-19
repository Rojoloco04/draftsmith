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
	"github.com/rojoloco04/draftsmith/internal/meta"
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
  draftsmith watch   [flags]          follow the local League client (champ select helper)
  draftsmith meta tools [--schema]    list OP.GG's meta tools
  draftsmith meta call <tool> [json]  call an OP.GG tool and cache the result
  draftsmith version

Config comes from flags, environment variables, or a .env file in the
current directory:
  RIOT_API_KEY        your Riot key (required for sync)
  DRAFTSMITH_RIOT_ID    Name#TAG; sync adds it to the tracked accounts

Several accounts can share one archive: run "draftsmith sync --riot-id
Name#TAG" once per account to add it. After that, sync and serve keep
every tracked account up to date, and the dashboard can switch between
them or show them together.
  DRAFTSMITH_PLATFORM   na1, euw1, kr, ... (default na1)
  DRAFTSMITH_DATA       archive directory (default ./data)
  DRAFTSMITH_LEAGUE_DIR League install directory, for watch

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
	case "watch":
		err = cmdWatch(ctx, os.Args[2:])
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
	if len(st.Accounts) == 0 {
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

// run adds the --riot-id account if it isn't tracked yet, then for every
// tracked account snapshots today's rank, archives new games, and saves
// state. Progress goes to out. The returned state lists every known
// account, even if syncing failed.
func (sy *syncer) run(ctx context.Context, out io.Writer) (store.State, error) {
	s := sy.store
	st, err := s.LoadState()
	if err != nil {
		return st, err
	}
	if sy.riotID != "" && !tracks(st, sy.riotID) {
		if err := sy.addAccount(ctx, &st, out); err != nil {
			return st, err
		}
	}
	if len(st.Accounts) == 0 {
		return st, errors.New("first sync needs --riot-id Name#TAG (or DRAFTSMITH_RIOT_ID)")
	}

	var errs []error
	for i := range st.Accounts {
		a := &st.Accounts[i]
		if a.Platform != "" && a.Platform != sy.client.Platform() {
			fmt.Fprintf(out, "skipping %s: it's on %s and this sync uses %s (--platform)\n", a.RiotID, a.Platform, sy.client.Platform())
			continue
		}
		if len(st.Accounts) > 1 {
			fmt.Fprintf(out, "%s:\n", a.RiotID)
		}
		if err := sy.runAccount(ctx, a, out); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.RiotID, err))
		}
		if err := s.SaveState(st); err != nil {
			return st, err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return st, errors.Join(errs...)
}

// tracks reports whether riotID is one of the tracked accounts.
func tracks(st store.State, riotID string) bool {
	for _, a := range st.Accounts {
		if strings.EqualFold(a.RiotID, riotID) {
			return true
		}
	}
	return false
}

// addAccount looks up sy.riotID and tracks it. An account already tracked
// under an older Riot ID is renamed instead.
func (sy *syncer) addAccount(ctx context.Context, st *store.State, out io.Writer) error {
	name, tag, ok := strings.Cut(sy.riotID, "#")
	if !ok {
		return fmt.Errorf("riot id %q should look like Name#TAG", sy.riotID)
	}
	acct, err := sy.client.AccountByRiotID(ctx, name, tag)
	if err != nil {
		return fmt.Errorf("looking up %s: %w", sy.riotID, err)
	}
	riotID := acct.GameName + "#" + acct.TagLine
	if a := st.Find(acct.PUUID); a != nil {
		fmt.Fprintf(out, "%s is now %s\n", a.RiotID, riotID)
		a.RiotID = riotID
	} else {
		st.Accounts = append(st.Accounts, store.Account{
			PUUID: acct.PUUID, RiotID: riotID, Platform: sy.client.Platform(), BackfillDone: map[int]bool{},
		})
		fmt.Fprintf(out, "tracking %s (%s)\n", riotID, sy.client.Platform())
	}
	return sy.store.SaveState(*st)
}

// runAccount snapshots one account's rank and archives its new games.
func (sy *syncer) runAccount(ctx context.Context, a *store.Account, out io.Writer) error {
	if entries, err := sy.client.LeagueEntries(ctx, a.PUUID); err != nil {
		fmt.Fprintf(out, "warning: could not snapshot rank: %v\n", err)
	} else if err := sy.store.PutRankSnapshot(a.PUUID, time.Now(), entries); err != nil {
		return err
	} else {
		for _, e := range entries {
			fmt.Fprintf(out, "%s: %s %s %d LP (%dW %dL)\n", e.QueueType, e.Tier, e.Rank, e.LeaguePoints, e.Wins, e.Losses)
		}
	}
	res, err := ingest.Sync(ctx, sy.client, sy.store, a, ingest.Options{
		Queues:    sy.queues,
		Timelines: sy.timelines,
		Max:       sy.max,
		Log:       out,
	})
	a.LastSync = time.Now()
	fmt.Fprintf(out, "archived %d matches and %d timelines (%d timelines no longer on Riot's side, %d errors)\n",
		res.NewMatches, res.NewTimelines, res.MissingTimelines, res.Errors)
	return err
}

// indexArchive brings the SQLite index in the data dir up to date.
func indexArchive(ctx context.Context, s *store.Store, st store.State, out io.Writer) error {
	d, err := db.OpenDir(ctx, s.Dir)
	if err != nil {
		return err
	}
	defer d.Close()
	res, err := d.Index(ctx, s, st.PUUIDs())
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
	if len(st.Accounts) == 0 {
		return errors.New("no archive yet; run `draftsmith sync --riot-id Name#TAG` first")
	}
	d, err := db.OpenDir(ctx, *data)
	if err != nil {
		return err
	}
	defer d.Close()
	start := time.Now()
	res, err := d.Rebuild(ctx, s, st.PUUIDs())
	if err != nil {
		return err
	}
	fmt.Printf("indexed %d games and %d rank snapshots into %s in %s",
		res.Added, res.Ranks, filepath.Join(*data, db.FileName), time.Since(start).Round(time.Millisecond))
	if res.Skipped > 0 {
		fmt.Printf(" (%d archived matches without %s skipped)", res.Skipped, st.RiotIDs())
	}
	fmt.Println()
	return nil
}

// loadForAnalysis reads the games of one account (Name#TAG) or, with
// account "" or "all", of every tracked account. It returns the accounts
// it read.
func loadForAnalysis(c common, account string) (*store.Store, []store.Account, []analysis.Game, error) {
	s, err := store.Open(c.data)
	if err != nil {
		return nil, nil, nil, err
	}
	st, err := s.LoadState()
	if err != nil {
		return nil, nil, nil, err
	}
	if len(st.Accounts) == 0 {
		return nil, nil, nil, errors.New("no archive yet; run `draftsmith sync --riot-id Name#TAG` first")
	}
	accts := st.Accounts
	if account != "" && !strings.EqualFold(account, "all") {
		accts = nil
		for _, a := range st.Accounts {
			if strings.EqualFold(a.RiotID, account) {
				accts = []store.Account{a}
			}
		}
		if accts == nil {
			return nil, nil, nil, fmt.Errorf("--account %s isn't tracked (tracked: %s)", account, st.RiotIDs())
		}
	}
	queues, err := c.queueList()
	if err != nil {
		return nil, nil, nil, err
	}
	qset := map[int]bool{}
	for _, q := range queues {
		qset[q] = true
	}
	puuids := make([]string, len(accts))
	for i, a := range accts {
		puuids[i] = a.PUUID
	}
	games, err := analysis.LoadGames(s, puuids, qset)
	return s, accts, games, err
}

const accountFlagHelp = "one account's games (Name#TAG), or all to combine every tracked account"

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	var c common
	c.register(fs)
	out := fs.String("out", "", "write to file instead of stdout")
	minGames := fs.Int("min-games", 5, "hide champions with fewer games")
	account := fs.String("account", "all", accountFlagHelp)
	fs.Parse(args)

	s, accts, games, err := loadForAnalysis(c, *account)
	if err != nil {
		return err
	}
	in := analysis.ReportInput{Games: games, MinGames: *minGames}
	want := "RANKED_SOLO_5x5"
	if c.queues == "flex" {
		want = "RANKED_FLEX_SR"
	}
	var names, ranks []string
	for _, a := range accts {
		names = append(names, a.RiotID)
		raw, day, err := s.LatestRankSnapshot(a.PUUID)
		if err != nil || raw == nil {
			continue
		}
		var entries []riot.LeagueEntry
		if json.Unmarshal(raw, &entries) != nil {
			continue
		}
		for _, e := range entries {
			if e.QueueType != want {
				continue
			}
			r := fmt.Sprintf("%s %s %d LP (%dW %dL)", e.Tier, e.Rank, e.LeaguePoints, e.Wins, e.Losses)
			if len(accts) > 1 {
				r = a.RiotID + " " + r
			}
			ranks = append(ranks, r)
			in.RankDate = max(in.RankDate, day)
		}
	}
	in.RiotID = strings.Join(names, ", ")
	in.Rank = strings.Join(ranks, "; ")

	sum := analysis.Summarize(games, in.MinGames)
	pm, err := poolMeta(c.data, sum)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: skipping pool vs meta:", err)
	}
	in.PoolMeta = pm

	w, closeFn, err := output(*out)
	if err != nil {
		return err
	}
	defer closeFn()
	analysis.RenderMarkdown(w, in, sum)
	return nil
}

// poolMeta renders the report's pool vs meta section from the OP.GG cache
// that serve fills. It never calls OP.GG, and returns "" if nothing is
// cached (or there's no index yet; it won't create one).
func poolMeta(data string, sum analysis.Summary) (string, error) {
	path := filepath.Join(data, db.FileName)
	if _, err := os.Stat(path); err != nil {
		return "", nil
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		return "", err
	}
	defer d.Close()
	tl, err := meta.NewCacheOnly(d).TierList(ctx)
	if errors.Is(err, meta.ErrNotCached) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	fits := meta.PoolFit(sum.Games, sum.Base, tl, meta.PoolMinGames)
	return meta.PoolMarkdown(fits, tl, sum.Base, time.Now()), nil
}

func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	var c common
	c.register(fs)
	out := fs.String("out", "", "write to file instead of stdout")
	account := fs.String("account", "all", accountFlagHelp)
	fs.Parse(args)

	_, _, games, err := loadForAnalysis(c, *account)
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
