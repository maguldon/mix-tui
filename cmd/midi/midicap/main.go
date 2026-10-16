// midicap records what a controller actually sends and diffs it against a
// mapping file, so a mapping is reconciled against hardware instead of against
// a plausible-looking table.
//
//	go run ./cmd/midi/midicap -map internal/testdata/mappings/xone-k2.yaml
//
// While it runs, type a label and press enter to attribute everything captured
// after it to that label. That is how an encoder bank gets recorded: the same
// physical knob sends different numbers per bank, so a capture with no bank
// label cannot be reconciled. Type `q`, or Ctrl-D on a piped run, to print the
// report.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"gitlab.com/gomidi/midi/v2/drivers"
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv"

	"github.com/maguldon/mix-tui/internal/midi"
)

func main() {
	mapPath := flag.String("map", "", "mapping file to reconcile against")
	filter := flag.String("device", "", "only capture ports whose name contains this (default: all)")
	live := flag.Bool("live", false, "print every message, not only each control's first sighting")
	flag.Parse()

	cap := newCapture()
	cap.live = *live
	done := make(chan struct{})
	var stopOnce sync.Once
	finish := func() { stopOnce.Do(func() { close(done) }) }

	devs, err := capturePorts(*filter, cap, done)
	if err != nil {
		fmt.Fprintf(os.Stderr, "midicap: %v\n", err)
		os.Exit(1)
	}
	if len(devs) == 0 {
		fmt.Fprintf(os.Stderr, "midicap: no MIDI input ports matched\n")
		os.Exit(1)
	}

	fmt.Printf("capturing %d port(s):\n", len(devs))
	for _, d := range devs {
		fmt.Printf("  %s\n", d)
	}
	fmt.Printf("\nType a label + enter before touching a control. `q` ends the session.\n\n")

	go readLabels(cap, done, finish)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		finish()
	}()

	<-done
	cap.report(*mapPath)
}

// observation is everything recorded about one distinct message.
type observation struct {
	label string
	count int
	min   uint8
	max   uint8
}

type capture struct {
	mu      sync.Mutex
	label   string
	live    bool
	seen    map[string]*observation
	labels  []string
	labeled map[string]bool
	other   map[string]int
}

// newCapture returns a recorder with a neutral starting label, so a control
// touched before the first label is still attributed somewhere.
func newCapture() *capture {
	return &capture{
		label:   "unlabelled",
		seen:    make(map[string]*observation),
		labeled: make(map[string]bool),
		other:   make(map[string]int),
	}
}

// key identifies a message the way a mapping file names one.
func key(m midi.Message) string {
	kind := "cc"
	if m.Kind == midi.KindNote {
		kind = "note"
	}
	return fmt.Sprintf("%s ch%d n%d", kind, m.Channel, m.Num)
}

// handle records one message against the label in effect.
func (c *capture) handle(m midi.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if m.Kind == midi.KindOther {
		c.other[m.Other.String()]++
		return
	}

	k := key(m)
	o, ok := c.seen[k]
	if !ok {
		v := m.ControlValue()
		o = &observation{label: c.label, min: v, max: v}
		c.seen[k] = o
		if !c.labeled[c.label] {
			c.labeled[c.label] = true
			c.labels = append(c.labels, c.label)
		}
		// Announce on first sight so the operator can anchor the surface's
		// layout while it is in their hands: touch one control, read the number,
		// repeat. A report that only prints at the end cannot be used that way.
		fmt.Printf("  NEW  %-16s (first seen under %q)\n", k, c.label)
	}
	v := m.ControlValue()
	o.count++
	if v < o.min {
		o.min = v
	}
	if v > o.max {
		o.max = v
	}
	// Repeats matter when the question is whether the same control changes
	// number under a different mode: a first-sight-only log would show CC0 once
	// and hide the fact that it also sent CC22.
	if c.live {
		fmt.Printf("  %s  %-16s value %3d\n",
			time.Now().Format("15:04:05.000"), k, v)
	}
}

func (c *capture) setLabel(l string) {
	c.mu.Lock()
	c.label = l
	c.mu.Unlock()
}

// readLabels reads stdin on its own goroutine so capture keeps running while
// the operator types.
//
// On a terminal the session ends at `q`. On a pipe, EOF ends it, so a scripted
// run against a virtual controller terminates on its own.
func readLabels(c *capture, done <-chan struct{}, finish func()) {
	sc := bufio.NewScanner(os.Stdin)
	piped := !isTerminal(os.Stdin)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch line {
		case "":
			continue
		case "q", "quit", "exit":
			finish()
			return
		}
		c.setLabel(line)
		fmt.Printf("\n--- label: %s ---\n", line)
	}
	if piped {
		finish()
	}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// capturePorts opens every matching input and feeds it to the recorder.
func capturePorts(filter string, c *capture, done <-chan struct{}) ([]string, error) {
	drv := drivers.Get()
	if drv == nil {
		return nil, fmt.Errorf("no MIDI driver available (is ALSA seq running?)")
	}
	ins, err := drv.Ins()
	if err != nil {
		return nil, err
	}

	var names []string
	for _, in := range ins {
		name := in.String()
		if filter != "" && !strings.Contains(strings.ToUpper(name), strings.ToUpper(filter)) {
			continue
		}
		port := in
		if err := port.Open(); err != nil {
			fmt.Fprintf(os.Stderr, "midicap: cannot open %s: %v\n", name, err)
			continue
		}
		stop, err := port.Listen(func(raw []byte, _ int32) {
			select {
			case <-done:
				return
			default:
			}
			c.handle(midi.Decode(raw))
		}, drivers.ListenConfig{})
		if err != nil {
			_ = port.Close()
			fmt.Fprintf(os.Stderr, "midicap: cannot listen on %s: %v\n", name, err)
			continue
		}
		names = append(names, name)
		go func() {
			<-done
			stop()
			_ = drv.Close()
		}()
	}
	sort.Strings(names)
	return names, nil
}

// declared is one binding from the mapping file under reconciliation.
type declared struct {
	key    string
	action string
	detail string
	mods   string
}

func (c *capture) report(mapPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	fmt.Printf("\n=== captured ===\n")
	seen := make(map[string]*observation, len(c.seen))
	for k, o := range c.seen {
		seen[k] = o
	}

	for _, label := range c.labels {
		fmt.Printf("\n--- %s ---\n", label)
		for _, k := range sortedKeys(seen) {
			o := seen[k]
			if o.label != label {
				continue
			}
			fmt.Printf("  %-16s %6d msgs  value %d..%d\n", k, o.count, o.min, o.max)
		}
	}

	if len(c.other) > 0 {
		fmt.Printf("\nignored by the control layer:\n")
		for _, k := range sortedCounts(c.other) {
			fmt.Printf("  %-16s %6d msgs\n", k, c.other[k])
		}
	}

	if mapPath == "" {
		return
	}
	c.diff(mapPath, seen)
}

// diff compares what the controller sent against what the file claims.
//
// Three verdicts, because each points at a different mistake:
//   - declared, never seen: the binding the file promises does not exist
//   - seen, not declared: a control the file says nothing about
//   - matches only at channel-1: the file is written 1-based, the app resolves
//     0-based, and every binding on that channel is silently dead
func (c *capture) diff(mapPath string, seen map[string]*observation) {
	data, err := os.ReadFile(mapPath)
	if err != nil {
		fmt.Printf("\n=== diff skipped ===\n  %v\n", err)
		return
	}
	m, err := midi.ParseMapping(mapPath, data)
	if err != nil {
		fmt.Printf("\n=== diff skipped ===\n  %v\n", err)
		return
	}

	var decl []declared
	for name, mod := range m.Modifiers {
		decl = append(decl, declared{
			key:    fmt.Sprintf("%s ch%d n%d", kindName(mod.Kind), mod.Chan, mod.Num),
			action: "modifier " + name,
			detail: fmt.Sprintf("pressed=%d released=%d", mod.Pressed, mod.Released),
			mods:   "",
		})
	}
	for _, ctl := range m.Controls {
		d := declared{
			key:    fmt.Sprintf("%s ch%d n%d", kindName(ctl.Kinds), ctl.Chan, ctl.Num),
			action: midi.FormatActionKind(ctl.Action),
			detail: fmt.Sprintf("target=%d", ctl.Target),
			mods:   strings.Join(ctl.Mods, "+"),
		}
		if ctl.Slot > 0 {
			d.detail += fmt.Sprintf(" slot=%d", ctl.Slot)
		}
		decl = append(decl, d)
	}

	fmt.Printf("\n=== %s vs %s ===\n\n", m.Device, mapPath)

	var missing, offsetOnly []declared
	for _, d := range decl {
		if _, ok := seen[d.key]; ok {
			continue
		}
		if _, ok := seen[shiftChan(d.key)]; ok {
			offsetOnly = append(offsetOnly, d)
			continue
		}
		missing = append(missing, d)
	}

	if len(missing) == 0 {
		fmt.Printf("declared and seen: every binding in the file fired.\n")
	} else {
		fmt.Printf("DECLARED BUT NEVER SENT (%d) -- these bindings cannot work:\n", len(missing))
		for _, d := range missing {
			fmt.Printf("  %-16s %-22s %s%s\n", d.key, d.action, d.detail, modsSuffix(d.mods))
		}
	}

	if len(offsetOnly) > 0 {
		fmt.Printf("\nSENT AT CHANNEL-1 ONLY (%d) -- the file's channel numbering does not match the wire:\n", len(offsetOnly))
		for _, d := range offsetOnly {
			fmt.Printf("  %-16s %-22s %s%s  (wire sends %s)\n",
				d.key, d.action, d.detail, modsSuffix(d.mods), shiftChan(d.key))
		}
	}

	declaredSet := make(map[string]bool, len(decl))
	for _, d := range decl {
		declaredSet[d.key] = true
	}
	var undeclared []string
	for k := range seen {
		if !declaredSet[k] {
			undeclared = append(undeclared, k)
		}
	}
	sort.Strings(undeclared)
	if len(undeclared) == 0 {
		fmt.Printf("\nsent but not declared: none.\n")
	} else {
		fmt.Printf("\nSENT BUT NOT DECLARED (%d) -- the file says nothing about these:\n", len(undeclared))
		for _, k := range undeclared {
			fmt.Printf("  %-16s label %q\n", k, seen[k].label)
		}
	}
}

func kindName(k midi.MsgKind) string {
	if k == midi.KindNote {
		return "note"
	}
	return "cc"
}

// shiftChan rewrites the channel of a key down by one, testing the hypothesis
// that a file was written 1-based while the wire is 0-based.
func shiftChan(k string) string {
	var kind string
	var ch, num int
	if _, err := fmt.Sscanf(k, "%s ch%d n%d", &kind, &ch, &num); err != nil {
		return k
	}
	return fmt.Sprintf("%s ch%d n%d", kind, ch-1, num)
}

func modsSuffix(mods string) string {
	if mods == "" {
		return ""
	}
	return "  mods=" + mods
}

func sortedKeys(m map[string]*observation) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedCounts(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
