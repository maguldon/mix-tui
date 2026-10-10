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

/* --- playback stream callbacks ------------------------------------------- */

extern void streamStateChanged(void *data, int32_t old, int32_t state,
                               void *error);
extern void streamParamChanged(void *data, uint32_t id, void *param);
extern void streamProcess(void *data);

void stream_state_trampoline(void *data, int32_t old, int32_t state,
                             const char *error)
{
	streamStateChanged(data, old, state, (void *)error);
}

void stream_param_trampoline(void *data, uint32_t id,
                             const struct spa_pod *param)
{
	streamParamChanged(data, id, (void *)param);
}

/* The realtime drain. This runs on libpipewire's data-loop thread. */
void stream_process_trampoline(void *data)
{
	streamProcess(data);
}

/* --- stream properties ---------------------------------------------------- */

/* pw_properties_new is variadic, and cgo cannot call a variadic C function at
 * all. Building the property list here keeps the Go side free of varargs and
 * keeps the pairing of keys to values in one place, where a mistake is visible
 * rather than a shift-by-one in an argument list.
 *
 * target may be NULL. The strings are copied by pw_properties_new. */
struct pw_properties *make_stream_props(const char *node_name,
                                        const char *target,
                                        int rate, int channels)
{
	struct pw_properties *props = NULL;

	if (node_name == NULL)
		return NULL;

	props = pw_properties_new(
		"node.name", node_name,
		"media.type", "Audio",
		"media.category", "Source",
		"media.role", "Music",
		/* Interleaved F32P matches dsp.Stereo, so the realtime drain is a
		 * straight copy with no deinterleave on the audio thread. */
		"audio.format", "F32P",
		NULL);

	if (props == NULL)
		return NULL;

	/* These take numbers, so they are formatted rather than passed as
	 * strings; spa parses either, but keeping them numeric here matches how
	 * the graph advertises them. */
	pw_properties_setf(props, "audio.rate", "%d", rate);
	pw_properties_setf(props, "audio.channels", "%d", channels);

	if (target != NULL && target[0] != '\0')
		pw_properties_set(props, "target.object", target);

	return props;
}

/* pw_stream_add_listener takes a void * user-data, and the Go side holds a
 * cgo.Handle index. Widening here keeps `go vet` from seeing a
 * uintptr-to-pointer conversion it would correctly refuse to bless. */
void add_stream_listener(struct pw_stream *stream, struct spa_hook *hook,
                         struct pw_stream_events *ev, uintptr_t handle)
{
	pw_stream_add_listener(stream, hook, ev, (void *)handle);
}
