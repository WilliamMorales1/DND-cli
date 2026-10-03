// Package fivetools searches D&D 5e reference data from the 5etools data files,
// cached on disk after first download.
package fivetools

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	baseURL        = "https://raw.githubusercontent.com/5etools-mirror-3/5etools-src/main/data/"
	cacheTTL       = 30 * 24 * time.Hour
	spellClassFile = "generated/gendata-spell-source-lookup.json"
)

// Spells and classes are listed in their own index.json instead.
var dataFiles = []string{
	"items.json", "items-base.json", "feats.json", "races.json", "backgrounds.json",
	"optionalfeatures.json", "conditionsdiseases.json", "actions.json", "variantrules.json",
}

// Kind is the record type, named after its key in the data files.
type Kind string

const (
	Spell           Kind = "spell"
	ClassFeature    Kind = "classFeature"
	SubclassFeature Kind = "subclassFeature"
	Feat            Kind = "feat"
	OptionalFeature Kind = "optionalfeature"
	Item            Kind = "item"
	BaseItem        Kind = "baseitem"
	Race            Kind = "race"
	Subrace         Kind = "subrace"
	Background      Kind = "background"
	Condition       Kind = "condition"
	Disease         Kind = "disease"
	Status          Kind = "status"
	Action          Kind = "action"
	VariantRule     Kind = "variantrule"
)

// kinds in preference order when several share a name.
var kinds = []Kind{
	Spell, ClassFeature, SubclassFeature, Feat, OptionalFeature, Item, BaseItem,
	Race, Subrace, Background, Condition, Disease, Status, Action, VariantRule,
}

var kindRank = func() map[Kind]int {
	m := make(map[Kind]int, len(kinds))
	for i, k := range kinds {
		m[k] = i
	}
	return m
}()

var kindLabels = map[Kind]string{
	Spell: "spell", ClassFeature: "class feature", SubclassFeature: "subclass feature",
	Feat: "feat", OptionalFeature: "option", Item: "item", BaseItem: "item",
	Race: "race", Subrace: "race", Background: "background", Condition: "condition",
	Disease: "disease", Status: "status", Action: "action", VariantRule: "rule",
}

func (k Kind) String() string { return kindLabels[k] }

// includes reports whether a search for k should return records of kind other.
func (k Kind) includes(other Kind) bool {
	return k == "" || k == other ||
		k == ClassFeature && other == SubclassFeature ||
		k == Item && other == BaseItem ||
		k == Race && other == Subrace
}

// ParseKind accepts a label or data key: "spell", "item", "feature", "rule", …
func ParseKind(s string) (Kind, bool) {
	s = strings.ToLower(s)
	switch s {
	case "feature":
		return ClassFeature, true
	case "invocation", "optionalfeature":
		return OptionalFeature, true
	}
	for _, k := range kinds {
		if s == strings.ToLower(string(k)) || s == kindLabels[k] {
			return k, true
		}
	}
	return "", false
}

// Entry is one record. Only fields shown in headers are decoded.
type Entry struct {
	Kind        Kind   `json:"-"`
	key         string // normalize(Name)
	Name        string `json:"name"`
	Source      string `json:"source"`
	Page        int    `json:"page"`
	Edition     string `json:"edition"`
	Entries     []any  `json:"entries"`
	HigherLevel []any  `json:"entriesHigherLevel"`
	Copy        *struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	} `json:"_copy"`

	// Spells and class features.
	Level      int            `json:"level"`
	School     string         `json:"school"`
	Time       []spellTime    `json:"time"`
	Range      jsontext.Value `json:"range"` // spells: object; items: "80/320"
	Components map[string]any `json:"components"`
	Duration   []spellDur     `json:"duration"`
	Meta       struct {
		Ritual bool `json:"ritual"`
	} `json:"meta"`
	Classes      []string `json:"-"`
	ClassName    string   `json:"className"`
	SubclassName string   `json:"subclassShortName"`

	// Items.
	Type        string   `json:"type"`
	Rarity      string   `json:"rarity"`
	ReqAttune   any      `json:"reqAttune"` // bool or condition string
	Wondrous    bool     `json:"wondrous"`
	Weight      float64  `json:"weight"`
	Value       *float64 `json:"value"` // copper pieces
	Dmg1        string   `json:"dmg1"`
	Dmg2        string   `json:"dmg2"`
	DmgType     string   `json:"dmgType"`
	Property    []any    `json:"property"`
	AC          int      `json:"ac"`
	BonusWeapon string   `json:"bonusWeapon"`
	BonusAC     string   `json:"bonusAc"`

	// Races.
	RaceName string `json:"raceName"`
	Speed    any    `json:"speed"`
}

type spellTime struct {
	Number    int    `json:"number"`
	Unit      string `json:"unit"`
	Condition string `json:"condition"`
}

type spellDur struct {
	Type          string `json:"type"`
	Concentration bool   `json:"concentration"`
	Duration      struct {
		Type   string `json:"type"`
		Amount int    `json:"amount"`
	} `json:"duration"`
	Ends []string `json:"ends"`
}

func (e *Entry) Is2024() bool {
	return e.Edition == "one" || slices.Contains([]string{"XPHB", "XDMG", "XMM"}, e.Source)
}

func (e *Entry) String() string {
	if e.ClassName != "" {
		return fmt.Sprintf("%s (%s %v, %s)", e.Name, cmp.Or(e.SubclassName, e.ClassName), e.Kind, e.Source)
	}
	return fmt.Sprintf("%s (%v, %s)", e.Name, e.Kind, e.Source)
}

type Library struct {
	entries []*Entry
	byKey   map[string][]*Entry // preferred first
}

// Open loads the library, refreshing files older than cacheTTL.
// A stale cached file is used when its download fails.
func Open(ctx context.Context, cacheDir string) (*Library, error) {
	f := fetcher{dir: cacheDir, client: &http.Client{Timeout: 30 * time.Second}}

	var files []string
	for _, index := range []string{"spells/index.json", "class/index.json"} {
		data, err := f.get(ctx, index)
		if err != nil {
			return nil, err
		}
		var names map[string]string
		if err := json.Unmarshal(data, &names); err != nil {
			return nil, fmt.Errorf("%s: %w", index, err)
		}
		for _, name := range names {
			files = append(files, filepath.Dir(index)+"/"+name)
		}
	}
	files = append(files, dataFiles...)
	files = append(files, spellClassFile)

	contents := make(map[string][]byte, len(files))
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 8)
	)
	for _, name := range files {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			data, err := f.get(ctx, name)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			} else {
				contents[name] = data
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return Parse(contents)
}

// Parse builds a library from data file contents keyed by path under data/.
func Parse(files map[string][]byte) (*Library, error) {
	lib := &Library{byKey: map[string][]*Entry{}}
	for name, data := range files {
		if name == spellClassFile {
			continue
		}
		if err := lib.parseFile(data); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	if data, ok := files[spellClassFile]; ok {
		if err := lib.addSpellClasses(data); err != nil {
			return nil, fmt.Errorf("%s: %w", spellClassFile, err)
		}
	}
	lib.resolveCopies()

	for _, e := range lib.entries {
		e.key = normalize(e.Name)
		lib.byKey[e.key] = append(lib.byKey[e.key], e)
	}
	for _, group := range lib.byKey {
		slices.SortStableFunc(group, preferred)
	}
	return lib, nil
}

// parseFile streams a data file, decoding the record list under each kind key.
func (lib *Library) parseFile(data []byte) error {
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return cmp.Or(err, errors.New("top level is not an object"))
	}
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		k := Kind(tok.String())
		if _, known := kindRank[k]; !known || dec.PeekKind() != '[' {
			if err := dec.SkipValue(); err != nil {
				return err
			}
			continue
		}
		dec.ReadToken() // [
		for dec.PeekKind() != ']' {
			rec, err := dec.ReadValue()
			if err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			if e := decodeEntry(rec); e != nil {
				e.Kind = k
				if k == Subrace {
					e.Name = fmt.Sprintf("%s (%s)", e.RaceName, e.Name)
				}
				lib.entries = append(lib.entries, e)
			}
		}
		if _, err := dec.ReadToken(); err != nil { // ]
			return err
		}
	}
	_, err := dec.ReadToken()
	return err
}

// decodeEntry falls back to name, source, and text when a record's fields
// don't fit Entry (actions use strings for "time"). Nameless records are nil.
func decodeEntry(rec jsontext.Value) *Entry {
	e := new(Entry)
	if json.Unmarshal(rec, e) != nil {
		var base struct {
			Name    string `json:"name"`
			Source  string `json:"source"`
			Page    int    `json:"page"`
			Entries []any  `json:"entries"`
		}
		if json.Unmarshal(rec, &base) != nil {
			return nil
		}
		e = &Entry{Name: base.Name, Source: base.Source, Page: base.Page, Entries: base.Entries}
	}
	if e.Name == "" {
		return nil
	}
	return e
}

// preferred orders by kind, then 2014 rules before 2024 reprints.
func preferred(a, b *Entry) int {
	return cmp.Or(
		cmp.Compare(kindRank[a.Kind], kindRank[b.Kind]),
		compareBool(a.Is2024(), b.Is2024()),
		strings.Compare(a.Source, b.Source),
	)
}

// compareBool sorts false first.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// addSpellClasses records which classes get each spell, including expanded lists.
func (lib *Library) addSpellClasses(data []byte) error {
	type lists struct {
		Class        map[string]map[string]any `json:"class"` // source -> class -> details
		ClassVariant map[string]map[string]any `json:"classVariant"`
	}
	var lookup map[string]map[string]lists // source -> spell -> lists
	if err := json.Unmarshal(data, &lookup); err != nil {
		return err
	}
	for _, e := range lib.entries {
		if e.Kind != Spell {
			continue
		}
		l := lookup[strings.ToLower(e.Source)][strings.ToLower(e.Name)]
		classes := map[string]bool{}
		for _, sources := range []map[string]map[string]any{l.Class, l.ClassVariant} {
			for _, group := range sources {
				for class := range group {
					classes[class] = true
				}
			}
		}
		e.Classes = slices.Sorted(maps.Keys(classes))
	}
	return nil
}

// resolveCopies gives "_copy" reprints the text of the entry they copy,
// without applying their modifications.
func (lib *Library) resolveCopies() {
	type id struct {
		kind         Kind
		name, source string
	}
	index := make(map[id]*Entry, len(lib.entries))
	for _, e := range lib.entries {
		index[id{e.Kind, strings.ToLower(e.Name), e.Source}] = e
	}
	for _, e := range lib.entries {
		for hops, cur := 0, e; e.Entries == nil && cur.Copy != nil && hops < 5; hops++ {
			src, ok := index[id{e.Kind, strings.ToLower(cur.Copy.Name), cur.Copy.Source}]
			if !ok {
				break
			}
			e.Entries, cur = src.Entries, src
		}
	}
}

// normalize drops case and apostrophes and turns other punctuation runs into
// single spaces: "Tasha's Fey-Touched" -> "tashas fey touched".
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '\'' || r == '’':
		case 'a' <= r && r <= 'z' || '0' <= r && r <= '9':
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

var parenthetical = regexp.MustCompile(`\([^)]*\)`)

// Result has a Match plus same-named Others, or else partial-match Candidates.
type Result struct {
	Match      *Entry
	Others     []*Entry
	Candidates []*Entry
}

type Query struct {
	Text  string // may end in "|SOURCE"
	Kind  Kind   // "" = any
	Class string // this class's features rank first
}

func (lib *Library) Search(q Query) Result {
	name, source, _ := strings.Cut(q.Text, "|")
	keep := func(e *Entry) bool {
		return q.Kind.includes(e.Kind) && (source == "" || strings.EqualFold(e.Source, source))
	}

	// Exact name, then without parentheticals: "Misty Step (Fey-Touched)".
	for _, text := range []string{name, parenthetical.ReplaceAllString(name, "")} {
		key := normalize(text)
		if key == "" {
			continue
		}
		group := slices.DeleteFunc(slices.Clone(lib.byKey[key]), func(e *Entry) bool { return !keep(e) })
		if len(group) > 0 {
			slices.SortStableFunc(group, func(a, b *Entry) int {
				return compareBool(!strings.EqualFold(a.ClassName, q.Class), !strings.EqualFold(b.ClassName, q.Class))
			})
			return Result{Match: group[0], Others: group[1:]}
		}
	}

	key := normalize(name)
	var cands []*Entry
	names := map[string]bool{}
	for _, e := range lib.entries {
		if strings.Contains(e.key, key) && keep(e) {
			cands = append(cands, e)
			names[e.key] = true
		}
	}

	// A unique containing name wins, else a unique prefix: "cure" -> Cure Wounds.
	var prefixed []string
	for n := range names {
		if strings.HasPrefix(n, key) {
			prefixed = append(prefixed, n)
		}
	}
	switch {
	case len(names) == 1:
		q.Text = slices.Collect(maps.Keys(names))[0] + "|" + source
		return lib.Search(q)
	case len(prefixed) == 1:
		q.Text = prefixed[0] + "|" + source
		return lib.Search(q)
	}
	slices.SortFunc(cands, func(a, b *Entry) int { return cmp.Or(strings.Compare(a.Name, b.Name), preferred(a, b)) })
	return Result{Candidates: cands}
}

type fetcher struct {
	dir    string
	client *http.Client
}

func (f fetcher) get(ctx context.Context, name string) ([]byte, error) {
	path := filepath.Join(f.dir, filepath.FromSlash(name))
	info, statErr := os.Stat(path)
	if statErr == nil && time.Since(info.ModTime()) < cacheTTL {
		return os.ReadFile(path)
	}
	data, err := f.download(ctx, name)
	if err != nil {
		if statErr == nil {
			return os.ReadFile(path)
		}
		return nil, err
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		_ = os.WriteFile(path, data, 0o644) // best effort
	}
	return data, nil
}

func (f fetcher) download(ctx context.Context, name string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+name, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", name, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
