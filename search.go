package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/term"

	"dnd/internal/fivetools"
)

const maxCandidates = 15

// openLibrary loads 5etools data once per run, on first use.
func openLibrary() func() (*fivetools.Library, error) {
	return sync.OnceValues(func() (*fivetools.Library, error) {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(dir, "dnd", "5etools")
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stdout, "  downloading 5etools data (first run only)...")
		}
		lib, err := fivetools.Open(context.Background(), dir)
		if err != nil {
			return nil, fmt.Errorf("5etools data unavailable: %w", err)
		}
		return lib, nil
	})
}

// search runs q, ranking the current character's class features first.
func (s *Session) search(q fivetools.Query) (fivetools.Result, error) {
	if s.Library == nil {
		return fivetools.Result{}, errors.New("5etools data unavailable")
	}
	lib, err := s.Library()
	if err != nil {
		return fivetools.Result{}, err
	}
	q.Class = s.Current.Class
	return lib.Search(q), nil
}

// findOne resolves q to a single entry, listing candidates otherwise.
func (s *Session) findOne(q fivetools.Query) (*fivetools.Entry, error) {
	res, err := s.search(q)
	if err != nil {
		return nil, err
	}
	if res.Match != nil {
		return res.Match, nil
	}
	return nil, candidatesError(q.Text, res.Candidates)
}

func candidatesError(query string, cands []*fivetools.Entry) error {
	if len(cands) == 0 {
		return fmt.Errorf("nothing found for %q", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d matches for %q:", len(cands), query)
	for _, e := range cands[:min(len(cands), maxCandidates)] {
		fmt.Fprintf(&b, "\n    %v", e)
	}
	if len(cands) > maxCandidates {
		fmt.Fprintf(&b, "\n    ... %d more, be more specific", len(cands)-maxCandidates)
	}
	return errors.New(b.String())
}

// termStyle returns the wrap width and whether ANSI bold is safe for stdout.
func termStyle() (width int, bold bool) {
	f, ok := stdout.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return 80, false
	}
	if w, _, err := term.GetSize(int(f.Fd())); err == nil {
		return min(w-2, 100), true
	}
	return 80, true
}

func cmdSearch(s *Session, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	var q fivetools.Query
	if k, ok := fivetools.ParseKind(args[0]); ok && len(args) > 1 {
		q.Kind, args = k, args[1:]
	}
	q.Text = strings.Join(args, " ")
	res, err := s.search(q)
	if err != nil {
		return err
	}
	e := res.Match
	if e == nil {
		return candidatesError(q.Text, res.Candidates)
	}

	width, bold := termStyle()
	fivetools.Render(stdout, e, width, bold)

	if n := len(res.Others); n > 0 {
		const shown = 4
		fmt.Fprint(stdout, "  also:")
		for _, o := range res.Others[:min(n, shown)] {
			fmt.Fprintf(stdout, " %v;", o)
		}
		if n > shown {
			fmt.Fprintf(stdout, " +%d more", n-shown)
		}
		fmt.Fprintf(stdout, "\n  (add |SOURCE to pick one, e.g. search %s|%s)\n", strings.ToLower(e.Name), strings.ToLower(res.Others[0].Source))
	}
	fmt.Fprintln(stdout)
	return nil
}

// searchOptions completes names from the current character's sheet.
func searchOptions(s *Session) []string {
	c := s.Current
	opts := slices.Concat(names(c.Attacks, attackName), resourceNames(s))
	if sc := c.Spellcasting; sc != nil {
		opts = append(opts, sc.Cantrips...)
		for _, list := range sc.Fixed {
			opts = append(opts, names(list.Spells, spellName)...)
		}
		opts = append(opts, preparedNames(s)...)
	}
	return slices.Concat(opts, c.Equipped, c.Inventory)
}
