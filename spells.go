package main

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"dnd/internal/fivetools"
)

type Spell struct {
	Name  string `json:"name"`
	Level int    `json:"level"`
}

// SpellList is a fixed group of spells, e.g. domain spells.
type SpellList struct {
	Source string
	Note   string
	Spells []Spell
}

type Spellcasting struct {
	Class     string // prepared spells must be on this class's list
	Ability   Ability
	MaxLevel  int
	PrepLimit int
	Cantrips  []string
	Fixed     []SpellList
	Prepared  []Spell
}

type Proficiencies struct {
	Armor, Weapons, Tools, Languages []string
}

func (c *Character) SpellAttack() int {
	return c.Prof() + c.Mod(c.Spellcasting.Ability)
}

func hasSpell(spells []Spell, name string) bool {
	return slices.ContainsFunc(spells, func(s Spell) bool { return strings.EqualFold(s.Name, name) })
}

func (sc *Spellcasting) isFixed(name string) bool {
	return slices.ContainsFunc(sc.Fixed, func(l SpellList) bool { return hasSpell(l.Spells, name) })
}

func (sc *Spellcasting) Prepare(spell *fivetools.Entry) error {
	switch {
	case spell.Level == 0:
		return fmt.Errorf("%s is a cantrip", spell.Name)
	case spell.Level > sc.MaxLevel:
		return fmt.Errorf("%s is %s level; highest slot is %s", spell.Name, fivetools.Ordinal(spell.Level), fivetools.Ordinal(sc.MaxLevel))
	case len(spell.Classes) > 0 && !slices.Contains(spell.Classes, sc.Class):
		return fmt.Errorf("%s is not on the %s spell list (%s)", spell.Name, sc.Class, strings.Join(spell.Classes, ", "))
	case sc.isFixed(spell.Name):
		return fmt.Errorf("%s is always prepared", spell.Name)
	case hasSpell(sc.Prepared, spell.Name):
		return fmt.Errorf("%s is already prepared", spell.Name)
	case len(sc.Prepared) >= sc.PrepLimit:
		return fmt.Errorf("already %d/%d prepared; unprepare one first", len(sc.Prepared), sc.PrepLimit)
	}
	sc.Prepared = append(sc.Prepared, Spell{spell.Name, spell.Level})
	slices.SortFunc(sc.Prepared, func(a, b Spell) int {
		return cmp.Or(cmp.Compare(a.Level, b.Level), strings.Compare(a.Name, b.Name))
	})
	return nil
}

// printByLevel writes one "1st: A, B" line per spell level.
func printByLevel(w io.Writer, spells []Spell) {
	sorted := slices.SortedStableFunc(slices.Values(spells), func(a, b Spell) int { return cmp.Compare(a.Level, b.Level) })
	for len(sorted) > 0 {
		n := 1
		for n < len(sorted) && sorted[n].Level == sorted[0].Level {
			n++
		}
		fmt.Fprintf(w, "    %s: %s\n", fivetools.Ordinal(sorted[0].Level), strings.Join(names(sorted[:n], spellName), ", "))
		sorted = sorted[n:]
	}
}

func (c *Character) PrintSpells(w io.Writer) {
	sc := c.Spellcasting
	if sc == nil {
		fmt.Fprintf(w, "  %s has no spellcasting\n", c.Name)
		return
	}
	fmt.Fprintf(w, "  %s spellcasting (%v): attack %+d, save DC %d, prepared %d/%d\n",
		sc.Class, sc.Ability, c.SpellAttack(), c.SpellDC(sc.Ability), len(sc.Prepared), sc.PrepLimit)
	c.PrintSlots(w)
	if len(sc.Cantrips) > 0 {
		fmt.Fprintf(w, "  Cantrips: %s\n", strings.Join(sc.Cantrips, ", "))
	}
	for _, list := range sc.Fixed {
		title := list.Source
		if list.Note != "" {
			title += " (" + list.Note + ")"
		}
		fmt.Fprintf(w, "  %s:\n", title)
		printByLevel(w, list.Spells)
	}
	fmt.Fprintln(w, "  Prepared:")
	if len(sc.Prepared) == 0 {
		fmt.Fprintln(w, "    none")
	}
	printByLevel(w, sc.Prepared)
}

func (c *Character) PrintProfs(w io.Writer) {
	var saves, skillList []string
	for a := range numAbilities {
		if c.SaveProfs.Has(a) {
			saves = append(saves, fmt.Sprintf("%v %+d", a, c.SaveMod(a)))
		}
	}
	for i := range skills {
		if s := Skill(i); c.SkillProfs.Has(s) {
			skillList = append(skillList, fmt.Sprintf("%v %+d", s, c.SkillMod(s)))
		}
	}
	p := c.Profs
	fmt.Fprintf(w, "  %s proficiencies (bonus %+d):\n", c.Name, c.Prof())
	for _, row := range []struct {
		title string
		items []string
	}{
		{"Saving throws", saves}, {"Skills", skillList}, {"Armor", p.Armor},
		{"Weapons", p.Weapons}, {"Tools", p.Tools}, {"Languages", p.Languages},
	} {
		fmt.Fprintf(w, "    %-14s %s\n", row.title+":", cmp.Or(strings.Join(row.items, ", "), "none recorded"))
	}
}

// casting returns the current character's spellcasting for a command needing args.
func casting(s *Session, args []string) (*Spellcasting, error) {
	switch {
	case len(args) == 0:
		return nil, errUsage
	case s.Current.Spellcasting == nil:
		return nil, fmt.Errorf("%s has no spellcasting", s.Current.Name)
	}
	return s.Current.Spellcasting, nil
}

func cmdPrepare(s *Session, args []string) error {
	sc, err := casting(s, args)
	if err != nil {
		return err
	}
	entry, err := s.findOne(fivetools.Query{Text: strings.Join(args, " "), Kind: fivetools.Spell})
	if err != nil {
		return err
	}
	if err := sc.Prepare(entry); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  prepared %s (%s level), %d/%d\n", entry.Name, fivetools.Ordinal(entry.Level), len(sc.Prepared), sc.PrepLimit)
	return nil
}

func cmdUnprepare(s *Session, args []string) error {
	sc, err := casting(s, args)
	if err != nil {
		return err
	}
	query := strings.Join(args, " ")
	i, err := lookup(sc.Prepared, spellName, query)
	if err != nil {
		return fmt.Errorf("prepared spell %q: %w", query, err)
	}
	name := sc.Prepared[i].Name
	sc.Prepared = slices.Delete(sc.Prepared, i, i+1)
	fmt.Fprintf(stdout, "  unprepared %s, %d/%d\n", name, len(sc.Prepared), sc.PrepLimit)
	return nil
}

func preparedNames(s *Session) []string {
	if s.Current.Spellcasting == nil {
		return nil
	}
	return names(s.Current.Spellcasting.Prepared, spellName)
}
