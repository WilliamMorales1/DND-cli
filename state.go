package main

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// saveFile holds everything that changes during play. Base stats stay in
// characters.go; the save only overlays mutable state, matched by name.
type saveFile struct {
	Current    string           `json:"current"`
	Characters []savedCharacter `json:"characters"`
}

type savedCharacter struct {
	Name      string         `json:"name"`
	HP        int            `json:"hp"`
	Gold      int            `json:"gold"`
	Resources map[string]int `json:"resources,omitempty"` // name -> uses left
	Equipped  []string       `json:"equipped,omitempty"`
	Inventory []string       `json:"inventory,omitempty"`
	Prepared  []Spell        `json:"prepared,omitzero"` // absent: keep the sheet's list
}

const saveName = "dnd.json"

// savePath puts the save next to the executable so it doesn't depend on the
// working directory. DND_SAVE overrides it (useful with `go run`).
func savePath() string {
	if p := os.Getenv("DND_SAVE"); p != "" {
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return saveName
	}
	return filepath.Join(filepath.Dir(exe), saveName)
}

// Load overlays the save at path onto chars and returns the saved current
// character. A missing file is not an error.
func Load(path string, chars []*Character) (*Character, error) {
	current := chars[0]
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return current, nil
	}
	if err != nil {
		return current, err
	}
	var save saveFile
	if err := json.Unmarshal(data, &save); err != nil {
		return current, err
	}

	byName := make(map[string]*Character, len(chars))
	for _, c := range chars {
		byName[c.Name] = c
	}
	for _, s := range save.Characters {
		c, ok := byName[s.Name]
		if !ok {
			continue
		}
		c.HP = min(max(s.HP, 0), c.MaxHP)
		c.Gold = s.Gold
		c.Equipped, c.Inventory = s.Equipped, s.Inventory
		if s.Prepared != nil && c.Spellcasting != nil {
			c.Spellcasting.Prepared = s.Prepared
		}
		for _, r := range c.Resources {
			if left, ok := s.Resources[r.Name]; ok {
				r.Left = min(max(left, 0), r.Max)
			}
		}
	}
	if c, ok := byName[save.Current]; ok {
		current = c
	}
	return current, nil
}

// Save writes state atomically: temp file in the same dir, then rename.
func Save(path string, chars []*Character, current *Character) error {
	save := saveFile{Current: current.Name}
	for _, c := range chars {
		res := make(map[string]int, len(c.Resources))
		for _, r := range c.Resources {
			res[r.Name] = r.Left
		}
		saved := savedCharacter{
			Name: c.Name, HP: c.HP, Gold: c.Gold,
			Resources: res, Equipped: c.Equipped, Inventory: c.Inventory,
		}
		if sc := c.Spellcasting; sc != nil {
			saved.Prepared = sc.Prepared
			if saved.Prepared == nil {
				saved.Prepared = []Spell{} // write [] not absent, so "none prepared" survives
			}
		}
		save.Characters = append(save.Characters, saved)
	}
	data, err := json.Marshal(save, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".dnd-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
