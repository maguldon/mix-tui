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
	"runtime/cgo"
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
func DefaultRate() (int, bool) {
	ensureInit()
	acquirePkg()
	defer releasePkg()

	loop := C.pw_thread_loop_new(nil, nil)
	if loop == nil {
		return 0, false
	}
	defer C.pw_thread_loop_destroy(loop)

	ctx := C.pw_context_new(C.pw_thread_loop_get_loop(loop), nil, 0)
	if ctx == nil {
		return 0, false
	}
	defer C.pw_context_destroy(ctx)

	core := C.pw_context_connect(ctx, nil, 0)
	if core == nil {
		return 0, false
	}
	defer C.pw_core_disconnect(core)

	if rc := C.pw_thread_loop_start(loop); rc != 0 {
		return 0, false
	}
	defer C.pw_thread_loop_stop(loop)

	registry := C.pw_core_get_registry(core, 0, 0)
	if registry == nil {
		return 0, false
	}

	probe := &rateProbe{}
	handle := cgo.NewHandle(probe)
	defer handle.Delete()

	metaName := C.CString("metadata")
	defer C.free(unsafe.Pointer(metaName))

	// PW_VERSION_METADATA is a version NUMBER (3), not a string; passing the
	// macro through CString would bind against garbage.
	meta := C.pw_registry_bind(registry, C.uint32_t(coreID), metaName,
		C.uint32_t(C.PW_VERSION_METADATA), 0)
	if meta == nil {
		return 0, false
	}
	defer C.pw_registry_destroy(registry, C.uint32_t(coreID))

	events := C.struct_pw_metadata_events{
		version:  C.uint32_t(C.PW_VERSION_METADATA_EVENTS),
		property: (*[0]byte)(unsafe.Pointer(C.rate_probe_trampoline)),
	}
	var hook C.struct_spa_hook
	// The handle is an opaque token C may retain for the life of the listener,
	// which is exactly what cgo.Handle exists for.
	C.add_meta_listener((*C.struct_pw_metadata)(unsafe.Pointer(meta)), &hook,
		&events, C.uintptr_t(handle))

	deadline := time.Now().Add(rateProbeTimeout)
	for {
		if rate, ok := probe.result(); ok {
			if rate < 8000 || rate > 192000 {
				// Out of range is treated as "not a rate" rather than
				// propagated: a nonsense value would size every buffer in
				// the engine wrong.
				return 0, false
			}
			return rate, true
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		time.Sleep(2 * time.Millisecond)
	}
}
