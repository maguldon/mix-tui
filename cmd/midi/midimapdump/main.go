// midimapdump prints every binding a mapping file defines, so a mis-mapped
// controller is diagnosable without attaching one.
//
//	midimapdump ~/.config/mix-tui/mappings/xone-k2.yaml
//	midimapdump -all ~/.config/mix-tui/mappings/
//	midimapdump -json xone-k2.yaml
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/midi"
)

type row struct {
	Chan   int    `json:"chan"`
	Kind   string `json:"kind"`
	Num    int    `json:"num"`
	Mods   string `json:"mods"`
	Action string `json:"action"`
	Slot   int    `json:"slot"`
	Mode   string `json:"mode"`
}

func main() {
	all := flag.Bool("all", false, "treat arguments as directories and dump every mapping in them")
	asJSON := flag.Bool("json", false, "emit machine-readable JSON")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: midimapdump [-all] [-json] <file.yaml|dir>...")
		os.Exit(2)
	}

	var files []string
	for _, a := range args {
		info, err := os.Stat(a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "midimapdump: %v\n", err)
			os.Exit(1)
		}
		if info.IsDir() && *all {
			matches, _ := filepath.Glob(filepath.Join(a, "*.y*ml"))
			files = append(files, matches...)
		} else {
			files = append(files, a)
		}
	}

	var rows []row
	failed := false
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "midimapdump: %s: %v\n", f, err)
			failed = true
			continue
		}
		m, err := midi.ParseMapping(f, data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "midimapdump: %v\n", err)
			failed = true
			continue
		}
		for _, c := range m.Controls {
			rows = append(rows, row{
				Chan:   int(c.Chan),
				Kind:   kindName(c.Kinds),
				Num:    int(c.Num),
				Mods:   strings.Join(c.Mods, ","),
				Action: actionName(c.Action),
				Slot:   c.Slot,
				Mode:   modeName(c.Mode),
			})
		}
		if !*asJSON && len(m.Modifiers) > 0 {
			fmt.Printf("# %s (device %q, match %s)\n", f, m.Device, matchName(m.Match))
			for _, name := range sortedMods(m.Modifiers) {
				src := m.Modifiers[name]
				fmt.Printf("#   mod %-8s %s ch%d n%d (%s)\n",
					name, kindName(src.Kind), src.Chan, src.Num, modModeName(src.ModMode))
			}
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Chan != b.Chan {
			return a.Chan < b.Chan
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Num != b.Num {
			return a.Num < b.Num
		}
		return a.Mods < b.Mods
	})

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			fmt.Fprintf(os.Stderr, "midimapdump: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Printf("%-4s %-5s %-5s %-14s %-22s %-5s %s\n", "ch", "kind", "num", "mods", "action", "slot", "mode")
		for _, r := range rows {
			fmt.Printf("%-4d %-5s %-5d %-14s %-22s %-5d %s\n",
				r.Chan, r.Kind, r.Num, r.Mods, r.Action, r.Slot, r.Mode)
		}
	}
	if failed {
		os.Exit(1)
	}
}

func sortedMods(m map[string]midi.ModSource) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func kindName(k midi.MsgKind) string {
	switch k {
	case midi.KindCC:
		return "cc"
	case midi.KindNote:
		return "note"
	default:
		return "other"
	}
}

func modeName(m midi.ValueMode) string {
	switch m {
	case midi.ModeAbsolute:
		return "absolute"
	case midi.ModeRelative:
		return "relative"
	case midi.ModeMomentary:
		return "momentary"
	case midi.ModeToggle:
		return "toggle"
	default:
		return "?"
	}
}

func modModeName(m midi.ModMode) string {
	switch m {
	case midi.ModMomentary:
		return "momentary"
	case midi.ModLatching:
		return "latching"
	case midi.ModToggleMod:
		return "toggle"
	default:
		return "?"
	}
}

func matchName(m midi.MatchKind) string {
	if m == midi.MatchExact {
		return "exact"
	}
	return "substring"
}

// actionName renders an action through the shared vocabulary so the dump shows
// exactly the spelling a mapping file would use.
func actionName(k engine.ActionKind) string { return midi.FormatActionKind(k) }
