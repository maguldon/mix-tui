//go:build linux

package pipewire

/*
// The include and link flags are named explicitly rather than requested via
// #cgo pkg-config, because Debian and Ubuntu ship a libspa-0.2.pc that adds
// -fno-strict-overflow. Go's cgo rejects any pkg-config flag it does not
// recognise, so the build fails before compiling a single line and every
// developer and CI job would have to set CGO_CFLAGS_ALLOW by hand. Naming the
// directories costs portability to an unusual install prefix, which is a far
// smaller price than a build that does not run as written.
#cgo CFLAGS: -I/usr/include/pipewire-0.3 -I/usr/include/spa-0.2 -D_REENTRANT
#cgo LDFLAGS: -lpipewire-0.3
#include <pipewire/pipewire.h>
#include <pipewire/extensions/metadata.h>
#include <spa/utils/dict.h>

// Defined in trampoline.c; a separate .c file is required because a static
// function in a cgo preamble cannot be referenced by the object cgo links for
// //export.
extern int rate_probe_trampoline(void *data, uint32_t subject,
                                 const char *key, const char *type,
                                 const char *property,
                                 const struct spa_dict *props);
extern int add_meta_listener(struct pw_metadata *m, struct spa_hook *hook,
                             struct pw_metadata_events *ev, uintptr_t handle);
*/
import "C"
import "C"

import (
	"strconv"
	"time"
	"unsafe"
)

// dictInt reads an integer out of an spa_dict by key.
//
// This is the parsing half of rate discovery, kept separate from the cgo
// plumbing so it can be tested without a daemon.
func dictInt(props unsafe.Pointer, key string) (int, bool) {
	if props == nil {
		return 0, false
	}
	d := (*C.struct_spa_dict)(props)
	if d == nil || d.items == nil || d.n_items == 0 {
		return 0, false
	}
	items := unsafe.Slice(d.items, int(d.n_items))
	ckey := C.CString(key)
	defer C.free(unsafe.Pointer(ckey))

	for i := range items {
		if C.strcmp(items[i].key, ckey) != 0 {
			continue
		}
		if items[i].value == nil {
			return 0, false
		}
		v, err := strconv.Atoi(goString((*byte)(unsafe.Pointer(items[i].value))))
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

// goString copies a NUL-terminated C string into a Go string.
func goString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	if n == 0 {
		return ""
	}
	buf := make([]byte, n)
	src := unsafe.Slice((*byte)(unsafe.Pointer(p)), n)
	copy(buf, src)
	return string(buf)
}

// rateProbeTimeout bounds how long DefaultRate waits for the asynchronous
// metadata event. The value is normally present the instant the listener
// attaches, so a slow answer is not worth a long stall at startup.
const rateProbeTimeout = 250 * time.Millisecond

// DefaultRate returns the sample rate the daemon runs its graph at.
//
// It reports (0, false) when the rate cannot be determined, and never returns
// an error: the failure mode is "caller falls back to 44100", not "caller
// gives up". A desktop that cannot answer this question is still perfectly
// usable, because PipeWire's graph resamples whatever we declare.
//
// HOW, because it is less obvious than it looks. PipeWire 1.6's C API has no
// pw_registry_get_property — the registry exposes only add_listener, bind and
// destroy. The daemon's default clock rate is published on the core metadata
// object, so the only supported route is to bind to that object and listen for
// property events. That is asynchronous: the value arrives on the thread loop
// after the listener is installed, which is why this polls with a deadline
// rather than reading it inline.
//
// The query is deliberately uncached. A device can be plugged in mid-session
// and change the graph's rate, and a stale answer would leave the engine
// resampling against a rate the device no longer uses.
//
// WHY IT IS STILL A STUB, and what was actually tried. The obvious routes are
// all dead ends, and rediscovering that costs an afternoon. Everything below
// was verified against PipeWire 1.6.8 in plain C, not just from Go:
//
//   - The rate is published exactly once: in the daemon's "settings" metadata
//     bag, under the key "clock.rate" on the core (subject 0). pw-cli info and
//     pw-dump both display it, which is what makes it look reachable.
//
//   - The core's REGISTRY global does not carry it. The registry's `global`
//     event delivers that global with exactly two properties, object.serial
//     and core.name. The `default.clock.rate` those tools print is the
//     settings bag merged over those props with a "default." prefix added by
//     the tool — which is why "default.clock.rate" is the wrong key to listen
//     for here.
//
//   - pw_core_create_object(core, "metadata", ...) returns a non-NULL object
//     that is not usable: add_listener succeeds and is never called,
//     set_property fails outright. Metadata objects are NOT shared by name.
//     Asking for the bag called "settings" gets a new empty bag belonging to
//     this client, not the daemon's.
//
//   - Binding the daemon's own settings global through the registry is
//     closer and is the only route whose listener is accepted at all.
//     pw_proxy_install_marshal has to come first, and only with
//     implementor=false — implementor=true makes add_listener fail. With that
//     combination installed the listener is still never invoked, and the
//     object refuses set_property because it holds only "r" permissions,
//     which is consistent with a read-only bind that never replays.
//
//   - Loading libpipewire-module-metadata into the client reaches a real
//     implementation, but reaching its global needs pw_global_get_object,
//     which libpipewire exports and this version's headers do not declare.
//     pw-cli imports exactly that pair, so it is presumably the way through.
//     It was not finished here.
//
// Until one of those is carried through, the fallback costs one resampler in
// the path (PipeWire's) rather than two. The declared and granted rates are
// both already visible at runtime — Stream.NegotiatedRate and
// Stats.RateMismatch, surfaced in the debug overlay — and audio.sample_rate in
// the config sets it explicitly.
func DefaultRate() (int, bool) {
	// Automatic negotiation is deferred, and this always reports "unknown".
	//
	// The value lives on the core metadata object, so the only supported route
	// is to bind to it and listen for property events — which requires a
	// running loop thread for the lifetime of the listener. Standing one up
	// and tearing it down here deadlocked in pw_thread_loop_stop against a
	// live daemon, and a rate query is not worth that at startup.
	//
	// The contract is unchanged and the consequence is benign: the caller
	// falls back to 44100 and PipeWire's graph resamples, which is one
	// resampler in the path instead of ours.
	return 0, false
}
