/* Trampolines bridging libpipewire's C callbacks to Go.
 *
 * These live in a real .c file rather than in a cgo preamble because cgo
 * copies its preamble into every generated translation unit: a `static`
 * function there cannot be referenced from the object cgo links for
 * //export, which fails at link time with an undefined reference.
 *
 * The Go side holds a runtime/cgo.Handle — an integer token, not an address —
 * and libpipewire retains the listener's user-data pointer for as long as the
 * listener is installed. That is exactly the case cgo.Handle exists for:
 * handing C an opaque token it may keep, with no Go pointer crossing the
 * boundary.
 */

#include <pipewire/pipewire.h>
#include <pipewire/extensions/metadata.h>
#include <spa/utils/dict.h>
#include <stdint.h>

/* The exported Go entry point, emitted by cgo into _cgo_export.c from the
 * //export directive in callbacks.go.
 *
 * It is declared by hand rather than via _cgo_export.h: on Go 1.27 that header
 * is empty for a function whose file carries no cgo import, so including it
 * yields an implicit declaration and the link fails. */
extern void rateProbeProperty(void *data, uint32_t subject,
                              void *key, void *type, void *property,
                              void *props);

/* Matches struct pw_metadata_events.property. */
int rate_probe_trampoline(void *data, uint32_t subject,
                          const char *key, const char *type,
                          const char *property,
                          const struct spa_dict *props)
{
	rateProbeProperty(data, subject, (void *)key, (void *)type,
	                  (void *)property, (void *)props);
	return 0;
}

/* pw_metadata_add_listener takes a void * user-data, but the Go side holds a
 * cgo.Handle index. Widening uintptr_t to void * is only sound because the
 * token is a handle index rather than a Go address, and doing it here rather
 * than in Go keeps `go vet` from seeing a uintptr-to-pointer conversion that
 * it would correctly refuse to bless. */
int add_meta_listener(struct pw_metadata *m, struct spa_hook *hook,
                      struct pw_metadata_events *ev, uintptr_t handle)
{
	return pw_metadata_add_listener(m, hook, ev, (void *)handle);
}
