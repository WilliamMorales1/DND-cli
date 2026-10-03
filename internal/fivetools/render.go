package fivetools

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"dnd/internal/wrap"
)

// Render writes e as wrapped terminal text. bold enables ANSI emphasis.
func Render(w io.Writer, e *Entry, width int, bold bool) {
	r := &renderer{w: w, width: max(width, 40), bold: bold}
	fmt.Fprintf(w, "\n  %s\n", r.em(e.Name))
	for _, line := range e.header() {
		r.hang("", line, 2)
	}
	fmt.Fprintln(w)
	r.entries(e.Entries, 2)
	r.entries(e.HigherLevel, 2)
	fmt.Fprintf(w, "  — %s", e.Source)
	if e.Page > 0 {
		fmt.Fprintf(w, " p. %d", e.Page)
	}
	fmt.Fprintln(w)
}

type renderer struct {
	w     io.Writer
	width int
	bold  bool
}

func (r *renderer) em(s string) string {
	if r.bold {
		return "\x1b[1m" + s + "\x1b[0m"
	}
	return s
}

func (r *renderer) entries(list []any, indent int) {
	for _, e := range list {
		r.entry(e, indent)
	}
}

func (r *renderer) entry(e any, indent int) {
	switch e := e.(type) {
	case string:
		r.para(StripTags(e), indent)
	case map[string]any:
		r.object(e, indent)
	}
}

func (r *renderer) object(e map[string]any, indent int) {
	name, _ := e["name"].(string)
	switch e["type"] {
	case "list":
		for _, it := range asList(e["items"]) {
			r.bullet(it, indent)
		}
		fmt.Fprintln(r.w)
	case "table":
		r.table(e, indent)
	case "refClassFeature", "refSubclassFeature", "refOptionalfeature", "refFeat":
		for _, key := range []string{"classFeature", "subclassFeature", "optionalfeature", "feat"} {
			if ref, ok := e[key].(string); ok {
				ref, _, _ = strings.Cut(ref, "|")
				r.para("• "+ref, indent)
			}
		}
	case "abilityDc":
		r.para(fmt.Sprintf("%s Save DC = 8 + your proficiency bonus + your %s modifier", name, joinAny(e["attributes"], " or ")), indent)
	case "abilityAttackMod":
		r.para(fmt.Sprintf("%s Attack modifier = your proficiency bonus + your %s modifier", name, joinAny(e["attributes"], " or ")), indent)
	default: // entries, section, inset, item, options, quote, variant, …
		body := blocks(e)
		if first, ok := firstString(body); ok && name != "" {
			r.para(r.em(StripTags(name)+".")+" "+StripTags(first), indent)
			r.entries(body[1:], indent)
			return
		}
		if name != "" {
			r.para(r.em(StripTags(name)), indent)
		}
		r.entries(body, indent+2*min(1, len(name)))
		r.entries(asList(e["items"]), indent)
	}
}

func (r *renderer) bullet(it any, indent int) {
	switch it := it.(type) {
	case string:
		r.hang("• ", StripTags(it), indent)
	case map[string]any:
		name, _ := it["name"].(string)
		body := blocks(it)
		text, ok := firstString(body)
		if !ok {
			r.hang("• ", StripTags(name), indent)
			r.entries(body, indent+2)
			return
		}
		text = StripTags(text)
		if name != "" {
			text = r.em(StripTags(name)) + " " + text
		}
		r.hang("• ", text, indent)
		r.entries(body[1:], indent+2)
	}
}

func (r *renderer) table(e map[string]any, indent int) {
	if caption, ok := e["caption"].(string); ok {
		r.para(r.em(StripTags(caption)), indent)
	}
	var rows [][]string
	if labels := asList(e["colLabels"]); len(labels) > 0 {
		rows = append(rows, cells(labels))
	}
	for _, row := range asList(e["rows"]) {
		if m, ok := row.(map[string]any); ok {
			row = m["row"]
		}
		rows = append(rows, cells(asList(row)))
	}
	widths := map[int]int{}
	for _, row := range rows {
		for i, c := range row {
			widths[i] = max(widths[i], len([]rune(c)))
		}
	}
	total := indent
	for _, w := range widths {
		total += w + 2
	}
	for i, row := range rows {
		if total > r.width { // too wide to align: one row per paragraph
			r.para(strings.Join(row, " | "), indent)
			continue
		}
		var b strings.Builder
		for j, c := range row {
			fmt.Fprintf(&b, "%-*s  ", widths[j], c)
		}
		line := strings.TrimRight(b.String(), " ")
		if i == 0 && len(asList(e["colLabels"])) > 0 {
			line = r.em(line)
		}
		fmt.Fprintf(r.w, "%s%s\n", strings.Repeat(" ", indent), line)
	}
	fmt.Fprintln(r.w)
}

func cells(list []any) []string {
	out := make([]string, len(list))
	for i, c := range list {
		switch c := c.(type) {
		case string:
			out[i] = StripTags(c)
		case map[string]any: // {"type": "cell", "roll": {"exact": 3} | {"min": 1, "max": 5}}
			if roll, ok := c["roll"].(map[string]any); ok {
				if exact, ok := roll["exact"]; ok {
					out[i] = num(exact)
				} else {
					out[i] = num(roll["min"]) + "–" + num(roll["max"])
				}
			} else if entry, ok := c["entry"].(string); ok {
				out[i] = StripTags(entry)
			}
		default:
			out[i] = fmt.Sprint(c)
		}
	}
	return out
}

func (r *renderer) para(text string, indent int) {
	r.hang("", text, indent)
	fmt.Fprintln(r.w)
}

func (r *renderer) hang(prefix, text string, indent int) {
	wrap.Hang(r.w, prefix, text, indent, r.width)
}

// innermost matches a tag with no tags inside it, so repeated passes unwrap nesting.
var innermost = regexp.MustCompile(`\{@(\w+)\s*([^{}]*)\}`)

// StripTags turns 5etools inline tags into plain text:
// "{@damage 8d6}" -> "8d6", "{@spell fireball|phb|Fireball!}" -> "Fireball!".
func StripTags(s string) string {
	for strings.Contains(s, "{@") {
		next := innermost.ReplaceAllStringFunc(s, func(m string) string {
			sub := innermost.FindStringSubmatch(m)
			return tagText(sub[1], strings.Split(sub[2], "|"))
		})
		if next == s {
			break
		}
		s = next
	}
	return s
}

var attackTags = map[string]string{
	"mw": "Melee Weapon Attack:", "rw": "Ranged Weapon Attack:", "mw,rw": "Melee or Ranged Weapon Attack:",
	"ms": "Melee Spell Attack:", "rs": "Ranged Spell Attack:", "ms,rs": "Melee or Ranged Spell Attack:",
	"m": "Melee Attack Roll:", "r": "Ranged Attack Roll:",
}

func tagText(tag string, parts []string) string {
	first := parts[0]
	switch tag {
	case "h":
		return "Hit: "
	case "m":
		return "Miss: "
	case "dc":
		return "DC " + first
	case "hit", "d20":
		if n, err := strconv.Atoi(first); err == nil {
			return fmt.Sprintf("%+d", n)
		}
		return first
	case "atk", "atkr":
		return attackTags[first]
	case "recharge":
		if first == "" {
			return "(Recharge 6)"
		}
		return "(Recharge " + first + "–6)"
	case "chance":
		if len(parts) > 1 && parts[1] != "" {
			return parts[1]
		}
		return first + " percent"
	case "scaledamage", "scaledice":
		if len(parts) > 2 {
			return parts[2]
		}
	case "damage", "dice", "autodice", "b", "bold", "i", "italic", "u", "s", "strike", "note", "color", "filter", "link", "5etools", "book", "adventure", "quickref":
		return first
	}
	// Reference tags ({@spell name|source|display}) show the display text if any.
	if len(parts) > 2 && parts[2] != "" {
		return parts[2]
	}
	return first
}

// blocks is an object's "entry" followed by its "entries".
func blocks(e map[string]any) []any {
	list := asList(e["entries"])
	if entry, ok := e["entry"]; ok {
		return append([]any{entry}, list...)
	}
	return list
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func firstString(list []any) (string, bool) {
	if len(list) == 0 {
		return "", false
	}
	s, ok := list[0].(string)
	return s, ok
}

func joinAny(v any, sep string) string {
	var parts []string
	for _, x := range asList(v) {
		parts = append(parts, strings.ToUpper(fmt.Sprint(x)))
	}
	return strings.Join(parts, sep)
}

func num(v any) string {
	if f, ok := v.(float64); ok && f == math.Trunc(f) {
		return strconv.Itoa(int(f))
	}
	return fmt.Sprint(v)
}

// header returns the kind-specific summary lines shown under the name.
func (e *Entry) header() []string {
	switch e.Kind {
	case Spell:
		return e.spellHeader()
	case Item, BaseItem:
		return e.itemHeader()
	case ClassFeature, SubclassFeature:
		class := e.ClassName
		if e.SubclassName != "" {
			class += " (" + e.SubclassName + ")"
		}
		return []string{fmt.Sprintf("%s feature, level %d", class, e.Level)}
	case Race, Subrace:
		if s := speed(e.Speed); s != "" {
			return []string{"Race. Speed " + s}
		}
	}
	return []string{capitalize(e.Kind.String())}
}

var schools = map[string]string{
	"A": "abjuration", "C": "conjuration", "D": "divination", "E": "enchantment",
	"V": "evocation", "I": "illusion", "N": "necromancy", "T": "transmutation", "P": "psionic",
}

// Ordinal formats n as "1st", "2nd", "11th", …
func Ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

func (e *Entry) spellHeader() []string {
	school := schools[e.School]
	title := capitalize(school) + " cantrip"
	if e.Level > 0 {
		title = Ordinal(e.Level) + "-level " + school
	}
	if e.Meta.Ritual {
		title += " (ritual)"
	}

	var times []string
	for _, t := range e.Time {
		unit := t.Unit
		if unit == "bonus" {
			unit = "bonus action"
		}
		s := plural(t.Number, unit)
		if t.Condition != "" {
			s += ", " + StripTags(t.Condition)
		}
		times = append(times, s)
	}

	var comps []string
	for _, k := range []string{"v", "s", "m"} {
		switch v := e.Components[k].(type) {
		case bool:
			if v {
				comps = append(comps, strings.ToUpper(k))
			}
		case string:
			comps = append(comps, "M ("+v+")")
		case map[string]any:
			comps = append(comps, fmt.Sprintf("M (%v)", v["text"]))
		}
	}

	var durs []string
	for _, d := range e.Duration {
		switch d.Type {
		case "instant":
			durs = append(durs, "Instantaneous")
		case "timed":
			s := plural(d.Duration.Amount, d.Duration.Type)
			if d.Concentration {
				s = "Concentration, up to " + s
			}
			durs = append(durs, s)
		case "permanent":
			s := "Until dispelled"
			if len(d.Ends) > 0 && !(len(d.Ends) == 1 && d.Ends[0] == "dispel") {
				s = "Until " + strings.Join(d.Ends, " or ") + "ed"
			}
			durs = append(durs, s)
		default:
			durs = append(durs, capitalize(d.Type))
		}
	}

	lines := []string{
		title,
		"Casting Time: " + strings.Join(times, " or "),
		"Range: " + e.spellRange(),
		"Components: " + strings.Join(comps, ", "),
		"Duration: " + strings.Join(durs, " or "),
	}
	if len(e.Classes) > 0 {
		lines = append(lines, "Classes: "+strings.Join(e.Classes, ", "))
	}
	return lines
}

func (e *Entry) spellRange() string {
	var rng struct {
		Type     string `json:"type"`
		Distance struct {
			Type   string `json:"type"`
			Amount int    `json:"amount"`
		} `json:"distance"`
	}
	if json.Unmarshal(e.Range, &rng) != nil {
		return ""
	}
	dist := rng.Distance
	switch rng.Type {
	case "point":
		if dist.Amount == 0 {
			return capitalize(dist.Type)
		}
		return plural(dist.Amount, strings.TrimSuffix(dist.Type, "s"))
	case "special":
		return "Special"
	}
	unit := strings.TrimSuffix(dist.Type, "s")
	if unit == "feet" {
		unit = "foot"
	}
	return fmt.Sprintf("Self (%d-%s %s)", dist.Amount, unit, rng.Type)
}

func plural(n int, unit string) string {
	if unit == "feet" || unit == "foot" {
		return fmt.Sprintf("%d feet", n)
	}
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

var itemTypes = map[string]string{
	"A": "ammunition", "AF": "ammunition", "AT": "artisan's tools", "EXP": "explosive", "FD": "food and drink",
	"G": "adventuring gear", "GS": "gaming set", "HA": "heavy armor", "INS": "instrument", "LA": "light armor",
	"M": "melee weapon", "MA": "medium armor", "MNT": "mount", "OTH": "other", "P": "potion", "R": "ranged weapon",
	"RD": "rod", "RG": "ring", "S": "shield", "SC": "scroll", "SCF": "spellcasting focus", "T": "tools",
	"TAH": "tack and harness", "TG": "trade good", "VEH": "vehicle", "WD": "wand", "$A": "art object",
	"$C": "treasure", "$G": "gemstone",
}

var damageTypes = map[string]string{
	"A": "acid", "B": "bludgeoning", "C": "cold", "F": "fire", "O": "force", "L": "lightning",
	"N": "necrotic", "P": "piercing", "I": "poison", "Y": "psychic", "R": "radiant", "S": "slashing", "T": "thunder",
}

var properties = map[string]string{
	"A": "ammunition", "AF": "ammunition", "BF": "burst fire", "F": "finesse", "H": "heavy", "L": "light",
	"LD": "loading", "R": "reach", "RLD": "reload", "S": "special", "T": "thrown", "2H": "two-handed", "V": "versatile",
}

func code(s string) string {
	c, _, _ := strings.Cut(s, "|")
	return c
}

func (e *Entry) itemHeader() []string {
	kind := itemTypes[code(e.Type)]
	if kind == "" && e.Wondrous {
		kind = "wondrous item"
	}
	kind = cmp.Or(kind, "item")
	if e.Rarity != "" && e.Rarity != "none" && e.Rarity != "unknown" {
		kind += ", " + e.Rarity
	}
	switch v := e.ReqAttune.(type) {
	case bool:
		if v {
			kind += " (requires attunement)"
		}
	case string:
		kind += " (requires attunement " + v + ")"
	}
	lines := []string{capitalize(kind)}

	var stats []string
	if e.Dmg1 != "" {
		s := e.Dmg1 + " " + damageTypes[e.DmgType]
		if e.BonusWeapon != "" {
			s = e.BonusWeapon + " weapon, " + s
		}
		stats = append(stats, s)
	}
	if e.AC > 0 {
		stats = append(stats, fmt.Sprintf("AC %d", e.AC))
	}
	if e.BonusAC != "" {
		stats = append(stats, e.BonusAC+" AC")
	}
	var props []string
	for _, p := range e.Property {
		s, ok := p.(string)
		if !ok {
			continue
		}
		c := code(s)
		name := cmp.Or(properties[c], c)
		if c == "V" && e.Dmg2 != "" {
			name += " (" + e.Dmg2 + ")"
		}
		var rng string
		if (c == "T" || c == "A") && json.Unmarshal(e.Range, &rng) == nil {
			name += " (range " + rng + ")"
		}
		props = append(props, name)
	}
	if len(props) > 0 {
		stats = append(stats, strings.Join(props, ", "))
	}
	if e.Weight > 0 {
		stats = append(stats, strconv.FormatFloat(e.Weight, 'f', -1, 64)+" lb.")
	}
	if e.Value != nil && *e.Value > 0 {
		stats = append(stats, strconv.FormatFloat(*e.Value/100, 'f', -1, 64)+" gp")
	}
	if len(stats) > 0 {
		lines = append(lines, strings.Join(stats, "; "))
	}
	return lines
}

func speed(v any) string {
	switch v := v.(type) {
	case float64:
		return num(v) + " ft."
	case map[string]any:
		var parts []string
		for _, mode := range []string{"walk", "burrow", "climb", "fly", "swim"} {
			if n, ok := v[mode].(float64); ok {
				s := num(n) + " ft."
				if mode != "walk" {
					s = mode + " " + s
				}
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
