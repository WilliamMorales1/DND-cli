package main

import (
	"fmt"
	"io"
	"strings"

	"dnd/internal/dice"
	"dnd/internal/wrap"
)

type Ability int

const (
	STR Ability = iota
	DEX
	CON
	INT
	WIS
	CHA
	numAbilities
)

var abilityNames = [numAbilities][2]string{
	{"STR", "strength"}, {"DEX", "dexterity"}, {"CON", "constitution"},
	{"INT", "intelligence"}, {"WIS", "wisdom"}, {"CHA", "charisma"},
}

func (a Ability) String() string { return abilityNames[a][0] }

// ParseAbility accepts the short or long name, any case.
func ParseAbility(s string) (Ability, bool) {
	for a, names := range abilityNames {
		if strings.EqualFold(s, names[0]) || strings.EqualFold(s, names[1]) {
			return Ability(a), true
		}
	}
	return 0, false
}

type Skill int

type skillInfo struct {
	name    string
	ability Ability
}

var skills = []skillInfo{
	{"Acrobatics", DEX}, {"Animal Handling", WIS}, {"Arcana", INT}, {"Athletics", STR},
	{"Deception", CHA}, {"History", INT}, {"Insight", WIS}, {"Intimidation", CHA},
	{"Investigation", INT}, {"Medicine", WIS}, {"Nature", INT}, {"Perception", WIS},
	{"Performance", CHA}, {"Persuasion", CHA}, {"Religion", INT}, {"Sleight of Hand", DEX},
	{"Stealth", DEX}, {"Survival", WIS},
}

const (
	Acrobatics Skill = iota
	AnimalHandling
	Arcana
	Athletics
	Deception
	History
	Insight
	Intimidation
	Investigation
	Medicine
	Nature
	Perception
	Performance
	Persuasion
	Religion
	SleightOfHand
	Stealth
	Survival
)

func (s Skill) String() string   { return skills[s].name }
func (s Skill) Ability() Ability { return skills[s].ability }

var squasher = strings.NewReplacer(" ", "", "_", "")

func squash(s string) string { return strings.ToLower(squasher.Replace(s)) }

// ParseSkill matches a prefix, ignoring case and spaces: "sleight", "perc".
func ParseSkill(s string) (Skill, bool) {
	needle := squash(s)
	for i, sk := range skills {
		if needle != "" && strings.HasPrefix(squash(sk.name), needle) {
			return Skill(i), true
		}
	}
	return 0, false
}

// Set is a bitset of small enum values.
type Set[T ~int] uint64

func SetOf[T ~int](vs ...T) Set[T] {
	var s Set[T]
	for _, v := range vs {
		s |= 1 << v
	}
	return s
}

func (s Set[T]) Has(v T) bool { return s&(1<<v) != 0 }

type Attack struct {
	Name    string
	Ability Ability
	Damage  dice.Dice // without the ability modifier
	Magic   int       // enhancement, added to to-hit and damage
	Extra   dice.Dice // extra dice rolled alongside, e.g. radiant; zero = none
	NoMod   bool      // don't add the ability modifier to damage (most leveled spells)
	Save    *Ability  // if set, no attack roll: target saves vs DC 8 + prof + mod
	Uses    string    // resource spent once per use
	Count   int       // rolls per use (e.g. Scorching Ray rays); 0 means 1
}

type Character struct {
	Name, Class  string
	Level        int
	Abilities    [numAbilities]int
	SaveProfs    Set[Ability]
	SaveBonus    int // flat bonus to all saves, e.g. Aura of Protection
	SkillProfs   Set[Skill]
	AC           int
	Speed        string
	MaxHP, HP    int
	Attacks      []Attack
	Resources    []*Resource
	Spellcasting *Spellcasting // nil for non-casters
	Profs        Proficiencies
	Gold         int
	Equipped     []string
	Inventory    []string
}

func abilityMod(score int) int { return score/2 - 5 }

func (c *Character) Prof() int             { return 2 + (max(c.Level, 1)-1)/4 }
func (c *Character) Mod(a Ability) int     { return abilityMod(c.Abilities[a]) }
func (c *Character) SpellDC(a Ability) int { return 8 + c.Prof() + c.Mod(a) }

func (c *Character) SaveMod(a Ability) int {
	m := c.Mod(a) + c.SaveBonus
	if c.SaveProfs.Has(a) {
		m += c.Prof()
	}
	return m
}

func (c *Character) SkillMod(s Skill) int {
	m := c.Mod(s.Ability())
	if c.SkillProfs.Has(s) {
		m += c.Prof()
	}
	return m
}

func (c *Character) ToHit(atk Attack) int { return c.Mod(atk.Ability) + c.Prof() + atk.Magic }

func (c *Character) DamageBonus(atk Attack) int {
	if atk.NoMod {
		return atk.Magic
	}
	return atk.Magic + c.Mod(atk.Ability)
}

func (c *Character) Damage(n int) {
	c.HP = max(c.HP-n, 0)
}

func (c *Character) Heal(n int) {
	c.HP = min(c.HP+n, c.MaxHP)
}

func star(b bool) string {
	if b {
		return "*"
	}
	return " "
}

func (c *Character) PrintSheet(w io.Writer) {
	fmt.Fprintf(w, "\n%s — level %d %s\n", c.Name, c.Level, c.Class)
	fmt.Fprintf(w, "  HP %d/%d   AC %d   Prof %+d   Init %+d   Speed %s\n",
		c.HP, c.MaxHP, c.AC, c.Prof(), c.Mod(DEX), c.Speed)
	if c.SaveBonus != 0 {
		fmt.Fprintf(w, "  Abilities (saves include %+d bonus):\n", c.SaveBonus)
	} else {
		fmt.Fprintln(w, "  Abilities:")
	}
	var items []string
	for a := range numAbilities {
		items = append(items, fmt.Sprintf("%v %2d (%+d)  save %+d%s",
			a, c.Abilities[a], c.Mod(a), c.SaveMod(a), star(c.SaveProfs.Has(a))))
	}
	printColumns(w, items)
	fmt.Fprintln(w, "  Skills (* = proficient):")
	items = items[:0]
	for i := range skills {
		s := Skill(i)
		items = append(items, fmt.Sprintf("%-16v %+d%s", s, c.SkillMod(s), star(c.SkillProfs.Has(s))))
	}
	printColumns(w, items)
	if len(c.Attacks) > 0 {
		fmt.Fprintln(w, "  Attacks:")
		for _, atk := range c.Attacks {
			if atk.Save != nil {
				fmt.Fprintf(w, "    %-24s DC %d %v save", atk.Name, c.SpellDC(atk.Ability), *atk.Save)
			} else {
				fmt.Fprintf(w, "    %-24s to-hit %+d", atk.Name, c.ToHit(atk))
			}
			fmt.Fprintf(w, "  dmg %v", atk.Damage.Plus(c.DamageBonus(atk)))
			if atk.Extra != (dice.Dice{}) {
				fmt.Fprintf(w, " + %v", atk.Extra)
			}
			if atk.Count > 1 {
				fmt.Fprintf(w, "  x%d", atk.Count)
			}
			if atk.Uses != "" {
				fmt.Fprintf(w, "  [uses %s]", atk.Uses)
			}
			fmt.Fprintln(w)
		}
	}
	c.PrintResources(w)
	fmt.Fprintf(w, "  Gold: %d gp\n", c.Gold)
	fmt.Fprintln(w)
}

func (c *Character) PrintInventory(w io.Writer) {
	fmt.Fprintf(w, "  Gold: %d gp\n", c.Gold)
	printList(w, "Equipped", c.Equipped)
	printList(w, "Inventory", c.Inventory)
}

func printList(w io.Writer, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(w, "  %s:\n", title)
	printColumns(w, items)
}

// printColumns lays items out in as many columns as the terminal fits.
func printColumns(w io.Writer, items []string) {
	width, _ := termStyle()
	wrap.Columns(w, items, 4, width)
}
