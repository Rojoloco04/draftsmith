package opgg

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseDocExample(t *testing.T) {
	text := `class LolListLaneMetaChampions: position_filter,data
class Data: positions
class Positions: mid
class Mid: champion,is_rip,play,win_rate,pick_rate,role_rate,ban_rate,kda,tier,rank

LolListLaneMetaChampions("mid",Data(Positions([Mid("Ahri",false,292854,0.51,0.11,0.96,0.04,2.58,1,1),Mid("Twisted Fate",false,138407,0.51,0.05,0.89,0.02,2.52,2,9)])))
OP.GG page: https://op.gg/lol/champions/...`
	got, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"position_filter": "mid",
		"data": map[string]any{
			"positions": map[string]any{
				"mid": []any{
					map[string]any{"champion": "Ahri", "is_rip": false, "play": 292854.0, "win_rate": 0.51, "pick_rate": 0.11, "role_rate": 0.96, "ban_rate": 0.04, "kda": 2.58, "tier": 1.0, "rank": 1.0},
					map[string]any{"champion": "Twisted Fate", "is_rip": false, "play": 138407.0, "win_rate": 0.51, "pick_rate": 0.05, "role_rate": 0.89, "ban_rate": 0.02, "kda": 2.52, "tier": 2.0, "rank": 9.0},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestParseValues(t *testing.T) {
	cases := []struct {
		expr string
		want any
	}{
		{`"plain"`, "plain"},
		{`"Vel'Koz"`, "Vel'Koz"},
		{`"say \"hi\"\n\\"`, "say \"hi\"\n\\"},
		{`'single "quoted"'`, `single "quoted"`},
		{`"Nunu & Willump"`, "Nunu & Willump"},
		{`"😀"`, "\U0001F600"},
		{`"Kai'Sa ✓"`, "Kai'Sa ✓"},
		{`-3.5`, -3.5},
		{`1e3`, 1000.0},
		{`0`, 0.0},
		{`true`, true},
		{`False`, false},
		{`null`, nil},
		{`None`, nil},
		{`[]`, []any{}},
		{`[ 1 , [2,"x"] , ]`, []any{1.0, []any{2.0, "x"}}},
		{`Empty()`, map[string]any{}},
	}
	for _, c := range cases {
		got, err := Parse("class Empty:\nclass Wrap: v\n\nWrap(" + c.expr + ")")
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if v := got.(map[string]any)["v"]; !reflect.DeepEqual(v, c.want) {
			t.Errorf("%s: got %#v, want %#v", c.expr, v, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"no header":         `Foo(1)`,
		"undeclared class":  "class A: x\n\nB(1)",
		"too few values":    "class A: x,y\n\nA(1)",
		"too many values":   "class A: x\n\nA(1,2)",
		"unterminated":      "class A: x\n\nA(\"abc)",
		"unclosed call":     "class A: x\n\nA(1",
		"unclosed list":     "class A: x\n\nA([1,2)",
		"missing comma":     "class A: x,y\n\nA(1 2)",
		"bare word":         "class A: x\n\nA(maybe)",
		"bad number":        "class A: x\n\nA(1.2.3)",
		"trailing junk":     "class A: x\n\nA(1)B",
		"bad class line":    "class A x\n\nA(1)",
		"empty":             "class A: x\n\n",
		"conflicting class": "class A: x\nclass A: y\n\nA(1)",
		"bad json":          `{"a": `,
	}
	for name, text := range cases {
		if _, err := Parse(text); err == nil {
			t.Errorf("%s: no error for %q", name, text)
		}
	}
}

func TestParseJSON(t *testing.T) {
	got, err := Parse(` {"summary": {"play": 12, "win_rate": 0.5123}}
OP.GG page: https://op.gg/x`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"summary": map[string]any{"play": 12.0, "win_rate": 0.5123}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The fixtures are real responses captured on 2026-11-18 (patch 16.22).

func TestDecodeRealTierList(t *testing.T) {
	type row struct {
		Champion string  `json:"champion"`
		Play     int     `json:"play"`
		Win      int     `json:"win"`
		PickRate float64 `json:"pick_rate"`
		BanRate  float64 `json:"ban_rate"`
		Tier     int     `json:"tier"`
		Rank     int     `json:"rank"`
	}
	var resp struct {
		Data struct {
			Positions map[string][]row `json:"positions"`
		} `json:"data"`
	}
	if err := Decode(readTestdata(t, "tierlist_all.txt"), &resp); err != nil {
		t.Fatal(err)
	}
	pos := resp.Data.Positions
	for _, p := range []string{"top", "mid", "jungle", "adc", "support"} {
		if len(pos[p]) < 30 {
			t.Errorf("%s: only %d champions", p, len(pos[p]))
		}
		for i, r := range pos[p] {
			if r.Champion == "" || r.Play <= 0 || r.Win <= 0 || r.Win > r.Play || r.Tier < 1 || r.Tier > 5 || r.Rank != i+1 {
				t.Errorf("%s[%d]: implausible row %+v", p, i, r)
				break
			}
		}
	}
	// The response declares only class Top and reuses it for every lane.
	if got := pos["top"][0]; got != (row{"Malphite", 172610, 89146, 0.07, 0.18, 1, 1}) {
		t.Errorf("top[0] = %+v", got)
	}
}

func TestDecodeRealChampions(t *testing.T) {
	var resp struct {
		Data struct {
			Champions []struct {
				ID   int    `json:"champion_id"`
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"champions"`
		} `json:"data"`
	}
	if err := Decode(readTestdata(t, "champions.txt"), &resp); err != nil {
		t.Fatal(err)
	}
	byKey := map[string]string{}
	for _, c := range resp.Data.Champions {
		byKey[c.Key] = c.Name
	}
	if len(byKey) < 170 {
		t.Errorf("only %d champions", len(byKey))
	}
	for key, name := range map[string]string{"Nunu": "Nunu & Willump", "Leblanc": "LeBlanc", "MonkeyKing": "Wukong", "Chogath": "Cho'Gath"} {
		if byKey[key] != name {
			t.Errorf("%s: got %q, want %q", key, byKey[key], name)
		}
	}
}

func TestDecodeRealAnalysis(t *testing.T) {
	type counter struct {
		Name string `json:"champion_name"`
		Play int    `json:"play"`
		Win  int    `json:"win"`
	}
	var resp struct {
		Data struct {
			DamageType     string    `json:"damage_type"`
			StrongCounters []counter `json:"strong_counters"`
			WeakCounters   []counter `json:"weak_counters"`
			Runes          struct {
				PrimaryPage int   `json:"primary_page_id"`
				Primary     []int `json:"primary_rune_ids"`
				StatMods    []int `json:"stat_mod_ids"`
			} `json:"runes"`
			Trends struct {
				Win struct {
					Version string `json:"version"`
				} `json:"win"`
			} `json:"trends"`
		} `json:"data"`
	}
	if err := Decode(readTestdata(t, "analysis_ahri_mid.txt"), &resp); err != nil {
		t.Fatal(err)
	}
	d := resp.Data
	if d.DamageType != "AP" || d.Trends.Win.Version != "16.22" || d.Runes.PrimaryPage != 8100 ||
		!reflect.DeepEqual(d.Runes.Primary, []int{8112, 8139, 8140, 8106}) || len(d.Runes.StatMods) != 3 {
		t.Errorf("unexpected analysis %+v", d)
	}
	// Weak counters reuse class StrongCounter.
	if len(d.WeakCounters) != 3 || d.WeakCounters[0] != (counter{"Katarina", 7187, 3481}) {
		t.Errorf("weak counters %+v", d.WeakCounters)
	}
}

func TestParseFieldDiagnostics(t *testing.T) {
	got, err := Parse(readTestdata(t, "analysis_diagnostics.txt"))
	if err != nil {
		t.Fatal(err)
	}
	diag, _ := got.(map[string]any)["_field_diagnostics"].(map[string]any)
	if !reflect.DeepEqual(diag["unmatched_fields"], []any{"data.trends.version"}) || !strings.Contains(diag["hint"].(string), `"[]"`) {
		t.Errorf("diagnostics %#v", diag)
	}
}

func TestDecodeFieldError(t *testing.T) {
	var v map[string]any
	err := Decode(readTestdata(t, "analysis_diagnostics.txt"), &v)
	var fe *FieldError
	if !errors.As(err, &fe) || !reflect.DeepEqual(fe.Fields, []string{"data.trends.version"}) {
		t.Errorf("got %v", err)
	}
}
