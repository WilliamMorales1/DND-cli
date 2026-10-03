// Package dice adds dice terms like "2d6" on top of Go's constant-expression
// calculator (go/types), so any arithmetic works around them.
package dice

import (
	"errors"
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"math"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
)

// MaxDice caps the number of dice in one expression.
const MaxDice = 1000

var ErrBadExpr = errors.New("bad dice expression")

// Dice is a parsed expression: Count dice with Sides faces, plus Bonus.
// Count == 0 means a flat number.
type Dice struct {
	Count, Sides, Bonus int
}

// Parse accepts "d20", "2d6", "1d8+3", "4d6-1", or a bare number "5".
func Parse(expr string) (Dice, error) {
	bad := func() (Dice, error) { return Dice{}, fmt.Errorf("%w: %q", ErrBadExpr, expr) }
	s := strings.ToLower(strings.TrimSpace(expr))
	countStr, rest, hasD := strings.Cut(s, "d")
	if !hasD {
		n, err := strconv.Atoi(s)
		if err != nil {
			return bad()
		}
		return Dice{Bonus: n}, nil
	}

	d := Dice{Count: 1}
	var err error
	if countStr != "" {
		if d.Count, err = strconv.Atoi(countStr); err != nil {
			return bad()
		}
	}
	sides, bonus := rest, ""
	if i := strings.IndexAny(rest, "+-"); i >= 0 {
		sides, bonus = rest[:i], rest[i:]
	}
	if d.Sides, err = strconv.Atoi(sides); err != nil {
		return bad()
	}
	if bonus != "" {
		if d.Bonus, err = strconv.Atoi(bonus); err != nil {
			return bad()
		}
	}
	if d.Count < 1 || d.Count > MaxDice || d.Sides < 2 {
		return bad()
	}
	return d, nil
}

// MustParse is Parse for expressions known valid at compile time.
func MustParse(expr string) Dice {
	d, err := Parse(expr)
	if err != nil {
		panic(err)
	}
	return d
}

// Plus returns d with n added to the bonus.
func (d Dice) Plus(n int) Dice {
	d.Bonus += n
	return d
}

// Crit returns d with the dice doubled (bonus unchanged).
func (d Dice) Crit() Dice {
	d.Count *= 2
	return d
}

func (d Dice) String() string {
	if d.Count == 0 {
		return strconv.Itoa(d.Bonus)
	}
	if d.Bonus == 0 {
		return fmt.Sprintf("%dd%d", d.Count, d.Sides)
	}
	return fmt.Sprintf("%dd%d%+d", d.Count, d.Sides, d.Bonus)
}

func Die(sides int) int { return rand.IntN(sides) + 1 }

// Roll evaluates d through Eval. d is valid by construction (Parse), so errors panic.
func (d Dice) Roll() Result {
	r, err := Eval(d.String())
	if err != nil {
		panic(err)
	}
	return r
}

// dieTerm matches NdM (N optional) as a whole word inside a larger expression.
var dieTerm = regexp.MustCompile(`(?i)\b(\d*)d(\d+)\b`)

// Term is one NdM in an expression and the dice it rolled.
type Term struct {
	Count, Sides int
	Rolls        []int
}

// Result is an evaluated expression.
type Result struct {
	Expr  string         // as given
	Shown string         // Expr with each dice term replaced by its rolls
	Terms []Term         // dice terms in order
	Value constant.Value // exact result: int, float, or bool (for comparisons)
}

// Eval rolls every NdM term in expr, substitutes the sums, and evaluates the
// rest as a Go constant expression via go/types: + - * / % << >> & | ^,
// parens, comparisons, float math, e.g. "2d6+3", "(d20+5)/2", "4d6 >= 15".
// Division of two integers truncates, like D&D rounding down.
func Eval(expr string) (Result, error) {
	r := Result{Expr: strings.TrimSpace(expr)}
	bad := func(why string) (Result, error) {
		return Result{}, fmt.Errorf("%w: %q: %s", ErrBadExpr, r.Expr, why)
	}
	if r.Expr == "" {
		return bad("empty")
	}

	var calc, shown strings.Builder
	last, dice := 0, 0
	for _, m := range dieTerm.FindAllStringSubmatchIndex(r.Expr, -1) {
		t := Term{Count: 1}
		var err error
		if m[2] != m[3] {
			if t.Count, err = strconv.Atoi(r.Expr[m[2]:m[3]]); err != nil {
				return bad(err.Error())
			}
		}
		if t.Sides, err = strconv.Atoi(r.Expr[m[4]:m[5]]); err != nil {
			return bad(err.Error())
		}
		if dice += t.Count; t.Count < 1 || t.Sides < 2 || dice > MaxDice {
			return bad(fmt.Sprintf("need 1-%d dice with 2+ sides", MaxDice))
		}

		sum := 0
		t.Rolls = make([]int, t.Count)
		for i := range t.Rolls {
			t.Rolls[i] = Die(t.Sides)
			sum += t.Rolls[i]
		}
		r.Terms = append(r.Terms, t)

		calc.WriteString(r.Expr[last:m[0]])
		fmt.Fprintf(&calc, "(%d)", sum)
		shown.WriteString(r.Expr[last:m[0]])
		shown.WriteString(fmt.Sprint(t.Rolls))
		last = m[1]
	}
	calc.WriteString(r.Expr[last:])
	shown.WriteString(r.Expr[last:])
	r.Shown = shown.String()

	tv, err := types.Eval(token.NewFileSet(), nil, token.NoPos, calc.String())
	if err != nil {
		return bad(err.Error())
	}
	if tv.Value == nil || tv.Value.Kind() == constant.String {
		return bad("not a number")
	}
	r.Value = tv.Value
	return r, nil
}

// Int is Value rounded down; 0 for booleans.
func (r Result) Int() int {
	f, _ := constant.Float64Val(constant.ToFloat(r.Value))
	return int(math.Floor(f))
}

// String renders like "2d6+3: [3 5]+3 = 11", or just "5" with no dice.
func (r Result) String() string {
	if len(r.Terms) == 0 {
		return r.Value.String()
	}
	return fmt.Sprintf("%s: %s = %v", r.Expr, r.Shown, r.Value)
}

// Mode is advantage state for a d20 roll.
type Mode int

const (
	Normal Mode = iota
	Advantage
	Disadvantage
)

// ParseMode accepts "", "n", "normal", "a", "adv", "advantage", "d", "dis", "disadvantage".
func ParseMode(s string) (Mode, bool) {
	switch strings.ToLower(s) {
	case "", "n", "normal":
		return Normal, true
	case "a", "adv", "advantage":
		return Advantage, true
	case "d", "dis", "disadvantage":
		return Disadvantage, true
	}
	return Normal, false
}

// Check is a d20 roll with a modifier.
type Check struct {
	Mode     Mode
	A, B     int // B is 0 for a normal roll
	Natural  int // kept die
	Modifier int
}

func D20(modifier int, mode Mode) Check {
	c := Check{Mode: mode, A: Die(20), Modifier: modifier}
	c.Natural = c.A
	switch mode {
	case Advantage:
		c.B = Die(20)
		c.Natural = max(c.A, c.B)
	case Disadvantage:
		c.B = Die(20)
		c.Natural = min(c.A, c.B)
	}
	return c
}

func (c Check) Total() int { return c.Natural + c.Modifier }

// String renders like "d20 (adv): 4, 17 -> 17 +5 = 22  NATURAL 20!".
func (c Check) String() string {
	var b strings.Builder
	switch c.Mode {
	case Advantage:
		fmt.Fprintf(&b, "d20 (adv): %d, %d -> %d", c.A, c.B, c.Natural)
	case Disadvantage:
		fmt.Fprintf(&b, "d20 (dis): %d, %d -> %d", c.A, c.B, c.Natural)
	default:
		fmt.Fprintf(&b, "d20: %d", c.A)
	}
	fmt.Fprintf(&b, " %+d = %d", c.Modifier, c.Total())
	switch c.Natural {
	case 20:
		b.WriteString("  NATURAL 20!")
	case 1:
		b.WriteString("  natural 1...")
	}
	return b.String()
}
