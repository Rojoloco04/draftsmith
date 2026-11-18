// Command lolstats archives your ranked League of Legends games from the
// Riot API and turns them into a report.
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
	"sort"
	"strings"
	"time"

	"github.com/Rojoloco04/lolstats/internal/analysis"
	"github.com/Rojoloco04/lolstats/internal/ingest"
	"github.com/Rojoloco04/lolstats/internal/opgg"
	"github.com/Rojoloco04/lolstats/internal/riot"
	"github.com/Rojoloco04/lolstats/internal/store"
)

const version = "0.1.0"

const usage = `lolstats: ranked League of Legends stats from your own match archive

Usage:
  lolstats sync    [flags]          archive ranked games (and today's rank) from the Riot API
  lolstats report  [flags]          print a markdown report from the archive
  lolstats export  [flags]          dump per-game features as JSON lines
  lolstats meta tools [--schema]    list OP.GG's meta tools
  lolstats meta call <tool> [json]  call an OP.GG tool and cache the result
  lolstats version

Config comes from flags, environment variables, or a .env file in the
current directory:
  RIOT_API_KEY        your Riot key (required for sync)
  LOLSTATS_RIOT_ID    Name#TAG
  LOLSTATS_PLATFORM   na1, euw1, kr, ... (default na1)
  LOLSTATS_DATA       archive directory (default ./data)

Run "lolstats <command> -h" for command flags.
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
	case "report":
		err = cmdReport(os.Args[2:])
	case "export":
		err = cmdExport(os.Args[2:])
	case "meta":
		err = cmdMeta(ctx, os.Args[2:])
	case "version":
		fmt.Println("lolstats", version)
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
	fs.StringVar(&c.data, "data", env("LOLSTATS_DATA", "data"), "archive directory")
	fs.StringVar(&c.platform, "platform", env("LOLSTATS_PLATFORM", "na1"), "platform: na1, euw1, kr, ...")
	fs.StringVar(&c.riotID, "riot-id", env("LOLSTATS_RIOT_ID", ""), "Riot ID as Name#TAG")
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

	key := os.Getenv("RIOT_API_KEY")
	if key == "" {
		return errors.New("RIOT_API_KEY is not set (get one at developer.riotgames.com)")
	}
	queues, err := c.queueList()
	if err != nil {
		return err
	}
	limits, err := riot.ParseLimits(*rate)
	if err != nil {
		return err
	}
	client, err := riot.NewClient(key, c.platform, limits)
	if err != nil {
		return err
	}
	if base := os.Getenv("LOLSTATS_RIOT_BASE_URL"); base != "" { // for tests
		client.BaseURL = func(string) string { return base }
	}
	s, err := store.Open(c.data)
	if err != nil {
		return err
	}
	st, err := s.LoadState()
	if err != nil {
		return err
	}

	if c.riotID != "" && !strings.EqualFold(c.riotID, st.RiotID) || st.PUUID == "" {
		if c.riotID == "" {
			return errors.New("first sync needs --riot-id Name#TAG (or LOLSTATS_RIOT_ID)")
		}
		name, tag, ok := strings.Cut(c.riotID, "#")
		if !ok {
			return fmt.Errorf("riot id %q should look like Name#TAG", c.riotID)
		}
		acct, err := client.AccountByRiotID(ctx, name, tag)
		if err != nil {
			return fmt.Errorf("looking up %s: %w", c.riotID, err)
		}
		if st.PUUID != "" && st.PUUID != acct.PUUID {
			return fmt.Errorf("%s holds games for a different account (%s); use a separate --data directory", c.data, st.RiotID)
		}
		st.PUUID = acct.PUUID
		st.RiotID = acct.GameName + "#" + acct.TagLine
		st.Platform = client.Platform()
		fmt.Printf("tracking %s (%s)\n", st.RiotID, st.Platform)
	}

	if entries, err := client.LeagueEntries(ctx, st.PUUID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not snapshot rank: %v\n", err)
	} else if err := s.PutRankSnapshot(time.Now(), entries); err != nil {
		return err
	} else {
		for _, e := range entries {
			fmt.Printf("%s: %s %s %d LP (%dW %dL)\n", e.QueueType, e.Tier, e.Rank, e.LeaguePoints, e.Wins, e.Losses)
		}
	}

	res, syncErr := ingest.Sync(ctx, client, s, &st, ingest.Options{
		Queues:    queues,
		Timelines: !*noTimelines,
		Max:       *max,
		Log:       os.Stdout,
	})
	st.LastSync = time.Now()
	if err := s.SaveState(st); err != nil {
		return err
	}
	fmt.Printf("archived %d matches and %d timelines (%d timelines no longer on Riot's side, %d errors)\n",
		res.NewMatches, res.NewTimelines, res.MissingTimelines, res.Errors)
	if errors.Is(syncErr, context.Canceled) {
		fmt.Println("interrupted; rerun sync to continue where it stopped")
		return nil
	}
	return syncErr
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
		return nil, st, nil, errors.New("no archive yet; run `lolstats sync --riot-id Name#TAG` first")
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
		return errors.New("usage: lolstats meta tools [--schema] | lolstats meta call <tool> [json-args]")
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
		data := fs.String("data", env("LOLSTATS_DATA", "data"), "archive directory (results are cached under meta/)")
		endpoint := fs.String("endpoint", opgg.DefaultEndpoint, "MCP endpoint")
		fs.Parse(args[1:])
		rest := fs.Args()
		if len(rest) == 0 {
			return errors.New("usage: lolstats meta call <tool> [json-args]")
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
