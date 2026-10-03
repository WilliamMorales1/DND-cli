package fivetools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = `{
	"spell": [
		{"name": "Fireball", "source": "PHB", "page": 241, "level": 3, "school": "V",
		 "time": [{"number": 1, "unit": "action"}],
		 "range": {"type": "point", "distance": {"type": "feet", "amount": 150}},
		 "components": {"v": true, "s": true, "m": "a tiny ball of bat guano and sulfur"},
		 "duration": [{"type": "instant"}],
		 "entries": ["Deals {@damage 8d6} fire damage. See {@spell fire bolt|phb|Fire Bolt}."]},
		{"name": "Fireball", "source": "XPHB", "level": 3, "school": "V", "entries": ["2024 text"]},
		{"name": "Misty Step", "source": "PHB", "level": 2, "school": "C", "entries": ["Teleport."]},
		{"name": "Cure Wounds", "source": "PHB", "level": 1, "entries": ["Heal."]},
		{"name": "Mass Cure Wounds", "source": "PHB", "level": 5, "entries": ["Heal more."]}
	],
	"item": [
		{"name": "Shield of Faith", "source": "X", "entries": ["an item"]},
		{"name": "Copy", "source": "Y", "_copy": {"name": "Shield of Faith", "source": "X"}}
	]
}`

func testLibrary(t *testing.T) *Library {
	t.Helper()
	lib, err := Parse(map[string][]byte{"test.json": []byte(fixture)})
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestSearch(t *testing.T) {
	lib := testLibrary(t)
	cases := []struct {
		query  string
		kind   Kind
		want   string // "Name|SOURCE" of Match, or "" for candidates
		others int
	}{
		{"fireball", "", "Fireball|PHB", 1},
		{"FIREBALL|xphb", "", "Fireball|XPHB", 0},
		{"misty-step", "", "Misty Step|PHB", 0},
		{"Misty Step (Fey-Touched)", "", "Misty Step|PHB", 0},
		{"misty", "", "Misty Step|PHB", 0},
		{"fireball", Item, "", 0},
		{"i", "", "", 0},
		{"cure", "", "Cure Wounds|PHB", 0},
		{"wounds", "", "", 0},
	}
	for _, c := range cases {
		res := lib.Search(Query{Text: c.query, Kind: c.kind})
		got := ""
		if res.Match != nil {
			got = res.Match.Name + "|" + res.Match.Source
		}
		if got != c.want || len(res.Others) != c.others {
			t.Errorf("Search(%q, %q) = %q with %d others; want %q with %d", c.query, c.kind, got, len(res.Others), c.want, c.others)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Fey-Touched":                   "fey touched",
		"  Tasha’s  Hideous--Laughter ": "tashas hideous laughter",
		"Misty Step (Fey-Touched)":      "misty step fey touched",
		"a ' b":                         "a b",
		"Ünïcödé 3rd":                   "n c d 3rd",
		"'":                             "",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCopyResolved(t *testing.T) {
	res := testLibrary(t).Search(Query{Text: "copy"})
	if res.Match == nil || len(res.Match.Entries) != 1 {
		t.Fatalf("copy not resolved: %+v", res)
	}
}

func TestStripTags(t *testing.T) {
	cases := map[string]string{
		"{@damage 8d6} fire":                       "8d6 fire",
		"{@spell fire bolt|phb|Fire Bolt}":         "Fire Bolt",
		"{@condition blinded}":                     "blinded",
		"{@dc 15} or {@hit 5}":                     "DC 15 or +5",
		"{@b bold {@i nested}}":                    "bold nested",
		"{@scaledamage 8d6|3-9|1d6} per slot":      "1d6 per slot",
		"{@atk mw} {@hit 4} to hit. {@h}5 damage":  "Melee Weapon Attack: +4 to hit. Hit: 5 damage",
		"{@filter 1st-level spell|spells|level=1}": "1st-level spell",
	}
	for in, want := range cases {
		if got := StripTags(in); got != want {
			t.Errorf("StripTags(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestRenderSpell(t *testing.T) {
	var b strings.Builder
	Render(&b, testLibrary(t).Search(Query{Text: "fireball"}).Match, 80, false)
	for _, want := range []string{"3rd-level evocation", "Range: 150 feet", "M (a tiny ball", "Deals 8d6 fire damage. See Fire Bolt.", "PHB p. 241"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("render missing %q:\n%s", want, b.String())
		}
	}
}

// TestRealData parses the live 5etools data. Set FIVETOOLS_ONLINE=1 to run.
func TestRealData(t *testing.T) {
	if os.Getenv("FIVETOOLS_ONLINE") == "" {
		t.Skip("set FIVETOOLS_ONLINE=1")
	}
	dir, _ := os.UserCacheDir()
	lib, err := Open(t.Context(), filepath.Join(dir, "dnd", "5etools"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range strings.Split(os.Getenv("FIVETOOLS_QUERIES"), ",") {
		if q == "" {
			continue
		}
		res := lib.Search(Query{Text: q, Class: "Cleric"})
		if res.Match == nil {
			t.Logf("%s: %d candidates %v", q, len(res.Candidates), res.Candidates[:min(5, len(res.Candidates))])
			continue
		}
		var b strings.Builder
		Render(&b, res.Match, 90, false)
		t.Logf("others %v%s", res.Others, b.String())
	}
}
