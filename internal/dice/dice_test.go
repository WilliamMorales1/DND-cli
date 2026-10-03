package dice

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	good := map[string]Dice{
		"d20":    {1, 20, 0},
		"2d6":    {2, 6, 0},
		"1D8+3":  {1, 8, 3},
		" 4d6-1": {4, 6, -1},
		"5":      {0, 0, 5},
		"-2":     {0, 0, -2},
	}
	for in, want := range good {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "d", "0d6", "1001d6", "2d1", "xd6", "2d6+", "2d6+x"} {
		if _, err := Parse(in); !errors.Is(err, ErrBadExpr) {
			t.Errorf("Parse(%q) err = %v; want ErrBadExpr", in, err)
		}
	}
}

func TestRollBounds(t *testing.T) {
	d := MustParse("3d6+2")
	for range 1000 {
		r := d.Roll()
		if n := r.Int(); n < 5 || n > 20 || len(r.Terms) != 1 || len(r.Terms[0].Rolls) != 3 {
			t.Fatalf("roll out of range: %v", r)
		}
	}
}

func TestEval(t *testing.T) {
	for in, want := range map[string]string{
		"1+2*3":       "7",
		"(1+2)*3":     "9",
		"7/2":         "3",
		"7/2.0":       "3.5",
		"2 > 1":       "true",
		"d20 - d20+0": "",
	} {
		r, err := Eval(in)
		if err != nil {
			t.Errorf("Eval(%q): %v", in, err)
			continue
		}
		if want != "" && r.Value.String() != want {
			t.Errorf("Eval(%q) = %v; want %s", in, r.Value, want)
		}
	}
	for range 1000 {
		r, err := Eval("(2d6 + d4) * 10")
		if n := r.Int(); err != nil || n < 30 || n > 160 || n%10 != 0 || len(r.Terms) != 2 {
			t.Fatalf("Eval = %v, %v", r, err)
		}
	}
	for _, in := range []string{"", "0d6", "2d1", "1001d6", "2d6+", "foo", `"x"`, "d6x"} {
		if _, err := Eval(in); !errors.Is(err, ErrBadExpr) {
			t.Errorf("Eval(%q) err = %v; want ErrBadExpr", in, err)
		}
	}
}

func TestD20Modes(t *testing.T) {
	for range 1000 {
		if c := D20(0, Advantage); c.Natural != max(c.A, c.B) {
			t.Fatalf("adv kept %d from %d, %d", c.Natural, c.A, c.B)
		}
		if c := D20(0, Disadvantage); c.Natural != min(c.A, c.B) {
			t.Fatalf("dis kept %d from %d, %d", c.Natural, c.A, c.B)
		}
	}
}
