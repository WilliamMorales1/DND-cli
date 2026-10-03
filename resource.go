package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"dnd/internal/dice"
)

type Reset int

const (
	NoReset   Reset = iota // never recharges (consumables, stored spells)
	ShortRest              // short or long rest
	LongRest               // long rest
	Daily                  // dawn/sunset; restored by a long rest
)

func (r Reset) String() string {
	return [...]string{"", "SR", "LR", "day"}[r]
}

// Resource is a limited-use feature, spell slot, pool, or item charge.
type Resource struct {
	Name      string
	Max, Left int
	Reset     Reset
	Dice      dice.Dice // rolled once per use spent; zero = none
	Heal      bool      // dice total (+CON mod per die) heals the character: hit dice
	Half      bool      // long rest only regains half of max (min 1): hit dice
	Also      string    // another resource spent alongside, e.g. Channel Divinity
	Note      string
}

func (r *Resource) String() string { return fmt.Sprintf("%s: %d/%d left", r.Name, r.Left, r.Max) }

var ErrNotFound = errors.New("not found")

// AmbiguousError lists every candidate a query matched.
type AmbiguousError struct{ Matches []string }

func (e *AmbiguousError) Error() string {
	return "ambiguous, matches:\n    " + strings.Join(e.Matches, "\n    ")
}

// lookup returns the index of an exact case-insensitive match, else of the
// only substring match.
func lookup[T any](items []T, name func(T) string, query string) (int, error) {
	query = strings.ToLower(query)
	found := -1
	var matches []string
	for i, it := range items {
		n := name(it)
		lower := strings.ToLower(n)
		if lower == query {
			return i, nil
		}
		if strings.Contains(lower, query) {
			found = i
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		return -1, ErrNotFound
	case 1:
		return found, nil
	}
	return -1, &AmbiguousError{matches}
}

func (c *Character) FindResource(query string) (*Resource, error) {
	i, err := lookup(c.Resources, resourceName, query)
	if err != nil {
		return nil, fmt.Errorf("resource %q: %w", query, err)
	}
	return c.Resources[i], nil
}

func (c *Character) FindAttack(query string) (Attack, error) {
	i, err := lookup(c.Attacks, attackName, query)
	if err != nil {
		return Attack{}, fmt.Errorf("attack %q: %w", query, err)
	}
	return c.Attacks[i], nil
}

// Spend uses n charges of r plus one of r.Also, rolling r.Dice per charge.
// Nothing changes unless all charges are available.
func (c *Character) Spend(w io.Writer, r *Resource, n int) error {
	if r.Left < n {
		return fmt.Errorf("not enough %s: %d/%d left", r.Name, r.Left, r.Max)
	}
	var also *Resource
	if r.Also != "" {
		var err error
		if also, err = c.FindResource(r.Also); err != nil {
			return err
		}
		if also.Left < 1 {
			return fmt.Errorf("%s also needs %s: 0/%d left", r.Name, also.Name, also.Max)
		}
		also.Left--
	}
	r.Left -= n
	fmt.Fprintf(w, "  %v\n", r)
	if also != nil {
		fmt.Fprintf(w, "  %v\n", also)
	}

	if r.Dice == (dice.Dice{}) {
		return nil
	}
	d := r.Dice
	if r.Heal {
		d = d.Plus(c.Mod(CON))
	}
	total := 0
	for range n {
		roll := d.Roll()
		fmt.Fprintf(w, "  %v\n", roll)
		total += max(roll.Int(), 0)
	}
	if r.Heal {
		c.Heal(total)
		fmt.Fprintf(w, "  healed %d, HP: %d/%d\n", total, c.HP, c.MaxHP)
	}
	return nil
}

func (r *Resource) Regain(n int) { r.Left = min(r.Left+n, r.Max) }

func (c *Character) Rest(long bool) {
	for _, r := range c.Resources {
		if r.Reset == NoReset || (!long && r.Reset != ShortRest) {
			continue
		}
		if r.Half {
			r.Regain(max(r.Max/2, 1))
		} else {
			r.Left = r.Max
		}
	}
	if long {
		c.HP = c.MaxHP
	}
}

func (c *Character) PrintResources(w io.Writer) {
	if len(c.Resources) == 0 {
		return
	}
	var other []*Resource
	for _, r := range c.Resources {
		if !r.IsSlot() {
			other = append(other, r)
		}
	}
	c.PrintSlots(w)
	if len(other) > 0 {
		fmt.Fprintln(w, "  Resources:")
		printColumns(w, names(other, (*Resource).Summary))
	}
}

// PrintSlots writes the spell slots, if any, in columns.
func (c *Character) PrintSlots(w io.Writer) {
	slots := c.SpellSlots()
	if len(slots) == 0 {
		return
	}
	fmt.Fprintln(w, "  Spell slots:")
	printColumns(w, names(slots, func(r *Resource) string { return fmt.Sprintf("%s %d/%d", r.Name, r.Left, r.Max) }))
}

// Summary is r's name, uses left, reset, and note on one line.
func (r *Resource) Summary() string {
	s := fmt.Sprintf("%s %d/%d", r.Name, r.Left, r.Max)
	if r.Reset != NoReset {
		s += " per " + r.Reset.String()
	}
	if r.Note != "" {
		s += " (" + r.Note + ")"
	}
	return s
}

// SpellSlots returns the resources that are spell slots, judged by name.
func (c *Character) SpellSlots() []*Resource {
	var out []*Resource
	for _, r := range c.Resources {
		if r.IsSlot() {
			out = append(out, r)
		}
	}
	return out
}

func (r *Resource) IsSlot() bool { return strings.Contains(strings.ToLower(r.Name), "slot") }
