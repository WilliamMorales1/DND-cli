# DND-go

Terminal D&D 5e character tracker: dice, checks, saves, attacks, limited-use
resources, rests, spells, inventory, and 5etools lookups. State is saved after
every change.

## Setup

Requires Go 1.27+.

```sh
git clone https://github.com/WilliamMorales1/DND-go.git
cd DND-go
cp characters.go.example characters.go   # then edit it with your own sheets
go build -o dnd .
./dnd
```

`characters.go` is gitignored, so your characters never end up in the repo.
The build fails with `undefined: Roster` until you create it.

## Characters

Each character is a Go function returning a `*Character`; `Roster()` lists
them. Sheets hold static stats only (ability scores, proficiencies, AC, max HP,
attacks, resources, spellcasting). Current HP, gold, resource uses, prepared
spells, and items live in the save file and are matched to sheets by `Name`,
so you can edit a sheet (level up, new resource) without losing progress.

See `characters.go.example` for a martial and a caster. Notes:

- `Class` is the class name only, e.g. `"Paladin"`. It ranks that class's
  features first in `search` results. Put species/subclass in comments.
- Dice use `dx("2d6+3")`.
- Name spell slot resources `1st Slot`, `2nd Slot`, ... to group them.
- An attack's `Uses` spends one use of the named resource.

## Save file

`dnd.json` is written next to the binary. Set `DND_SAVE` to put it elsewhere
(useful with `go run .`):

```sh
DND_SAVE=$HOME/.local/share/dnd.json go run .
```

## 5etools data

`search` and `prepare` download 5etools data on first use into your user cache
directory (`~/.cache/dnd/5etools` on Linux).

## Commands

Type `help` in the app for the full list. Common ones:

```
roll 2d6+3          sheet               chars / use <name>
check dex adv       skill stealth       attack longsword
init                dmg 7 / heal 5      res / spend <resource> [n]
rest short|long     inv / gold +50      add <item> / equip <item>
spells              prepare <spell>     search spell fireball
```
