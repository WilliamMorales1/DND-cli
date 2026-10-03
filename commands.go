package main

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"dnd/internal/dice"
	"dnd/internal/fivetools"
)

type Session struct {
	Chars    []*Character
	Current  *Character
	SavePath string
	Library  func() (*fivetools.Library, error) // nil disables search
}

var (
	errUsage = errors.New("usage")
	errQuit  = errors.New("quit")
)

type command struct {
	names    []string // canonical name first
	args     string
	help     string // empty: listed with the previous command, same args
	mutates  bool   // save after running
	mode     bool   // takes a trailing adv|dis
	complete func(s *Session) []string
	run      func(s *Session, args []string) error
}

var commands []*command

// Set in init because cmdHelp and Complete read commands.
func init() {
	commands = []*command{
		{names: []string{"roll", "r"}, args: "<dice>", help: "roll/calc, e.g. 2d6+3, 4d6 >= 15", run: cmdRoll},
		{names: []string{"chars"}, help: "list characters", run: cmdChars},
		{names: []string{"use"}, args: "<name>", help: "pick character", mutates: true,
			complete: func(s *Session) []string { return names(s.Chars, charName) },
			run:      cmdUse},
		{names: []string{"sheet"}, help: "character sheet", run: show((*Character).PrintSheet)},
		{names: []string{"check"}, args: "<ability> [adv|dis]", help: "ability check / saving throw", mode: true,
			complete: abilityList, run: cmdCheck(false)},
		{names: []string{"save"}, args: "<ability> [adv|dis]", mode: true,
			complete: abilityList, run: cmdCheck(true)},
		{names: []string{"skill"}, args: "<skill> [adv|dis]", help: "skill check", mode: true,
			complete: func(*Session) []string { return names(skills, func(s skillInfo) string { return s.name }) },
			run:      cmdSkill},
		{names: []string{"attack", "atk"}, args: "<name> [adv|dis]", help: "attack + damage",
			mutates: true, mode: true,
			complete: func(s *Session) []string { return names(s.Current.Attacks, attackName) },
			run:      cmdAttack},
		{names: []string{"init"}, args: "[adv|dis]", help: "initiative",
			complete: func(*Session) []string { return []string{"adv", "dis"} },
			run:      cmdInit},
		{names: []string{"dmg"}, args: "<n>", help: "damage / heal HP", mutates: true, run: cmdHP((*Character).Damage)},
		{names: []string{"heal"}, args: "<n>", mutates: true, run: cmdHP((*Character).Heal)},
		{names: []string{"res"}, help: "limited uses", run: show((*Character).PrintResources)},
		{names: []string{"spend"}, args: "<resource> [n]", help: "spend / regain resource uses", mutates: true,
			complete: resourceNames, run: cmdSpend(true)},
		{names: []string{"regain"}, args: "<resource> [n]", mutates: true,
			complete: resourceNames, run: cmdSpend(false)},
		{names: []string{"rest"}, args: "short|long", help: "short or long rest", mutates: true,
			complete: func(*Session) []string { return []string{"short", "long"} },
			run:      cmdRest},
		{names: []string{"inv"}, help: "inventory", run: show((*Character).PrintInventory)},
		{names: []string{"gold"}, args: "[+n|-n|n]", help: "show/change gold", mutates: true, run: cmdGold},
		{names: []string{"add"}, args: "<item>", help: "add / remove item", mutates: true, run: cmdAdd},
		{names: []string{"rm"}, args: "<item>", mutates: true,
			complete: func(s *Session) []string { return slices.Concat(s.Current.Inventory, s.Current.Equipped) },
			run:      cmdRemove},
		{names: []string{"equip"}, args: "<item>", help: "equip / unequip item", mutates: true,
			complete: func(s *Session) []string { return s.Current.Inventory },
			run:      cmdEquip(true)},
		{names: []string{"unequip"}, args: "<item>", mutates: true,
			complete: func(s *Session) []string { return s.Current.Equipped },
			run:      cmdEquip(false)},
		{names: []string{"spells"}, help: "list spells", run: show((*Character).PrintSpells)},
		{names: []string{"prepare", "prep"}, args: "<spell>", help: "prepare / unprepare spell", mutates: true,
			run: cmdPrepare},
		{names: []string{"unprepare", "unprep"}, args: "<spell>", mutates: true,
			complete: preparedNames, run: cmdUnprepare},
		{names: []string{"profs"}, help: "proficiencies",
			run: show((*Character).PrintProfs)},
		{names: []string{"search", "lookup"}, args: "[kind] <name>[|source]", help: "5etools lookup",
			complete: searchOptions, run: cmdSearch},
		{names: []string{"help", "h", "?"}, help: "this list", run: cmdHelp},
		{names: []string{"quit", "exit", "q"}, help: "exit", run: func(*Session, []string) error { return errQuit }},
	}
}

func lookupCommand(name string) *command {
	name = strings.ToLower(name)
	for _, c := range commands {
		if slices.Contains(c.names, name) {
			return c
		}
	}
	return nil
}

func (c *command) usage() string { return strings.TrimSpace(c.names[0] + " " + c.args) }

// Exec runs one input line, returning errQuit to stop.
func (s *Session) Exec(line string) error {
	args := strings.Fields(line)
	if len(args) == 0 {
		return nil
	}
	cmd := lookupCommand(args[0])
	if cmd == nil {
		fmt.Fprintln(stdout, "  unknown command, type 'help'")
		return nil
	}
	switch err := cmd.run(s, args[1:]); {
	case errors.Is(err, errQuit):
		return err
	case errors.Is(err, errUsage):
		fmt.Fprintf(stdout, "  usage: %s\n", cmd.usage())
	case err != nil:
		fmt.Fprintf(stdout, "  %v\n", err)
	}
	if cmd.mutates {
		if err := Save(s.SavePath, s.Chars, s.Current); err != nil {
			fmt.Fprintf(stdout, "  save failed: %v\n", err)
		}
	}
	return nil
}

func show(printTo func(*Character, io.Writer)) func(*Session, []string) error {
	return func(s *Session, _ []string) error {
		printTo(s.Current, stdout)
		return nil
	}
}

func names[T any](items []T, name func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = name(it)
	}
	return out
}

func charName(c *Character) string    { return c.Name }
func attackName(a Attack) string      { return a.Name }
func resourceName(r *Resource) string { return r.Name }
func spellName(s Spell) string        { return s.Name }

func abilityList(*Session) []string {
	out := make([]string, numAbilities)
	for a := range numAbilities {
		out[a] = a.String()
	}
	return out
}

func resourceNames(s *Session) []string { return names(s.Current.Resources, resourceName) }

// splitMode peels a trailing adv|dis off args. Names may contain spaces.
func splitMode(args []string) (string, dice.Mode) {
	if len(args) > 1 {
		if m, ok := dice.ParseMode(args[len(args)-1]); ok {
			return strings.Join(args[:len(args)-1], " "), m
		}
	}
	return strings.Join(args, " "), dice.Normal
}

// splitCount peels a trailing number off args, defaulting to 1.
func splitCount(args []string) (string, int) {
	if len(args) > 1 {
		if n, err := strconv.Atoi(args[len(args)-1]); err == nil {
			return strings.Join(args[:len(args)-1], " "), n
		}
	}
	return strings.Join(args, " "), 1
}

func cmdRoll(_ *Session, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	r, err := dice.Eval(strings.Join(args, " "))
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  %v\n", r)
	return nil
}

func cmdChars(s *Session, _ []string) error {
	for _, c := range s.Chars {
		mark := " "
		if c == s.Current {
			mark = ">"
		}
		fmt.Fprintf(stdout, "  %s %s (level %d %s)\n", mark, c.Name, c.Level, c.Class)
	}
	return nil
}

func cmdUse(s *Session, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	query := strings.Join(args, " ")
	i, err := lookup(s.Chars, charName, query)
	if err != nil {
		return fmt.Errorf("character %q: %w", query, err)
	}
	s.Current = s.Chars[i]
	fmt.Fprintf(stdout, "  now playing %s\n", s.Current.Name)
	return nil
}

func cmdCheck(save bool) func(*Session, []string) error {
	return func(s *Session, args []string) error {
		name, mode := splitMode(args)
		a, ok := ParseAbility(name)
		if !ok {
			return errUsage
		}
		kind, mod := "check", s.Current.Mod(a)
		if save {
			kind, mod = "save", s.Current.SaveMod(a)
		}
		fmt.Fprintf(stdout, "  %s %v %s\n  %v\n", s.Current.Name, a, kind, dice.D20(mod, mode))
		return nil
	}
}

func cmdSkill(s *Session, args []string) error {
	name, mode := splitMode(args)
	sk, ok := ParseSkill(name)
	if !ok {
		return errUsage
	}
	fmt.Fprintf(stdout, "  %s %v check\n  %v\n", s.Current.Name, sk, dice.D20(s.Current.SkillMod(sk), mode))
	return nil
}

func cmdInit(s *Session, args []string) error {
	mode, ok := dice.ParseMode(strings.Join(args, ""))
	if !ok {
		return errUsage
	}
	fmt.Fprintf(stdout, "  %s initiative\n  %v\n", s.Current.Name, dice.D20(s.Current.Mod(DEX), mode))
	return nil
}

func cmdAttack(s *Session, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	name, mode := splitMode(args)
	c := s.Current
	atk, err := c.FindAttack(name)
	if err != nil {
		return err
	}
	if atk.Uses != "" {
		r, err := c.FindResource(atk.Uses)
		if err != nil {
			return err
		}
		if err := c.Spend(stdout, r, 1); err != nil {
			return err
		}
	}
	count := max(atk.Count, 1)
	for i := range count {
		if count > 1 {
			fmt.Fprintf(stdout, "  -- %d of %d --\n", i+1, count)
		}
		rollAttack(c, atk, mode)
	}
	return nil
}

func rollAttack(c *Character, atk Attack, mode dice.Mode) {
	crit := false
	if atk.Save != nil {
		fmt.Fprintf(stdout, "  %s uses %s: DC %d %v save\n", c.Name, atk.Name, c.SpellDC(atk.Ability), *atk.Save)
	} else {
		check := dice.D20(c.ToHit(atk), mode)
		fmt.Fprintf(stdout, "  %s attacks with %s\n  %v\n", c.Name, atk.Name, check)
		if check.Natural == 1 {
			fmt.Fprintln(stdout, "  miss")
			return
		}
		crit = check.Natural == 20
	}

	dmg, extra := atk.Damage.Plus(c.DamageBonus(atk)), atk.Extra
	if crit {
		dmg, extra = dmg.Crit(), extra.Crit()
	}
	fmt.Fprintf(stdout, "  damage: %v\n", dmg.Roll())
	if extra != (dice.Dice{}) {
		fmt.Fprintf(stdout, "  extra: %v\n", extra.Roll())
	}
}

func cmdHP(apply func(*Character, int)) func(*Session, []string) error {
	return func(s *Session, args []string) error {
		if len(args) != 1 {
			return errUsage
		}
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 0 {
			return errUsage
		}
		c := s.Current
		apply(c, n)
		fmt.Fprintf(stdout, "  %s HP: %d/%d\n", c.Name, c.HP, c.MaxHP)
		if c.HP == 0 {
			fmt.Fprintln(stdout, "  down! roll death saves with 'roll d20'")
		}
		return nil
	}
}

func cmdSpend(spend bool) func(*Session, []string) error {
	return func(s *Session, args []string) error {
		if len(args) == 0 {
			return errUsage
		}
		name, n := splitCount(args)
		if n < 1 {
			return errors.New("count must be at least 1")
		}
		r, err := s.Current.FindResource(name)
		if err != nil {
			return err
		}
		if spend {
			return s.Current.Spend(stdout, r, n)
		}
		r.Regain(n)
		fmt.Fprintf(stdout, "  %v\n", r)
		return nil
	}
}

func cmdRest(s *Session, args []string) error {
	var kind string
	switch strings.ToLower(strings.Join(args, "")) {
	case "short", "s":
		kind = "short"
	case "long", "l":
		kind = "long"
	default:
		return errUsage
	}
	s.Current.Rest(kind == "long")
	fmt.Fprintf(stdout, "  %s finishes a %s rest\n", s.Current.Name, kind)
	s.Current.PrintResources(stdout)
	return nil
}

func cmdGold(s *Session, args []string) error {
	c := s.Current
	switch len(args) {
	case 0:
	case 1:
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return errUsage
		}
		if args[0][0] == '+' || args[0][0] == '-' {
			c.Gold += n
		} else {
			c.Gold = n
		}
		if c.Gold < 0 {
			fmt.Fprintln(stdout, "  warning: gold is negative")
		}
	default:
		return errUsage
	}
	fmt.Fprintf(stdout, "  Gold: %d gp\n", c.Gold)
	return nil
}

func cmdAdd(s *Session, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	item := strings.Join(args, " ")
	s.Current.Inventory = append(s.Current.Inventory, item)
	fmt.Fprintf(stdout, "  added %s\n", item)
	return nil
}

// takeItem removes the item matching query from list.
func takeItem(list *[]string, query string) (string, error) {
	i, err := lookup(*list, func(s string) string { return s }, query)
	if err != nil {
		return "", fmt.Errorf("item %q: %w", query, err)
	}
	item := (*list)[i]
	*list = slices.Delete(*list, i, i+1)
	return item, nil
}

func cmdRemove(s *Session, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	c, query := s.Current, strings.Join(args, " ")
	item, err := takeItem(&c.Inventory, query)
	if errors.Is(err, ErrNotFound) {
		item, err = takeItem(&c.Equipped, query)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  removed %s\n", item)
	return nil
}

func cmdEquip(equip bool) func(*Session, []string) error {
	return func(s *Session, args []string) error {
		if len(args) == 0 {
			return errUsage
		}
		c := s.Current
		from, to, verb := &c.Equipped, &c.Inventory, "unequipped"
		if equip {
			from, to, verb = to, from, "equipped"
		}
		item, err := takeItem(from, strings.Join(args, " "))
		if err != nil {
			return err
		}
		*to = append(*to, item)
		fmt.Fprintf(stdout, "  %s %s\n", verb, item)
		return nil
	}
}

func cmdHelp(*Session, []string) error {
	fmt.Fprintln(stdout, "Commands:")
	for i, c := range commands {
		if c.help == "" {
			continue
		}
		name := c.names[0]
		for _, next := range commands[i+1:] {
			if next.help != "" {
				break
			}
			name += "|" + next.names[0]
		}
		fmt.Fprintf(stdout, "  %-31s %s\n", strings.TrimSpace(name+" "+c.args), c.help)
	}
	fmt.Fprintln(stdout, "Tab, Right, End complete.")
	return nil
}

// Complete suggests command names, then the command's arguments, then a
// trailing adv|dis for commands that take one.
func (s *Session) Complete(line string) []string {
	lower := strings.ToLower(line)
	name, arg, hasArg := strings.Cut(lower, " ")
	var out []string
	if !hasArg {
		for _, c := range commands {
			if n := c.names[0]; strings.HasPrefix(n, name) {
				if c.args != "" {
					n += " "
				}
				out = append(out, n)
			}
		}
		return out
	}

	cmd := lookupCommand(name)
	if cmd == nil || cmd.complete == nil {
		return nil
	}
	head := lower[:len(name)+1]
	for _, opt := range cmd.complete(s) {
		opt = strings.ToLower(opt)
		if strings.HasPrefix(opt, arg) {
			out = append(out, head+opt)
		} else if rest, ok := strings.CutPrefix(arg, opt+" "); ok && cmd.mode {
			for _, m := range []string{"adv", "dis"} {
				if strings.HasPrefix(m, rest) {
					out = append(out, head+opt+" "+m)
				}
			}
		}
	}
	return out
}
