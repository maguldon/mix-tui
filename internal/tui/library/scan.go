package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// audioExts are the formats the decoder accepts, and only those.
//
// This must track audio.Decode exactly. Listing .m4a or .aiff here indexed
// files that appear in the library and then fail on every load, which is worse
// than not listing them: the user sees a track and pressing enter produces an
// error.
var audioExts = map[string]bool{
	".wav": true, ".mp3": true, ".flac": true, ".ogg": true,
}

// skipDirs are directories never worth walking.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".cache": true,
	"@eaDir": true, ".Trash": true, "$RECYCLE.BIN": true,
}

// Scan walks the roots, publishing each track through onTrack as it is found
// and progress through onProgress.
//
// It never aborts on a bad root: errors are collected and returned joined at
// the end, so one missing path costs that path rather than the whole library
// (spec §8). Scan decodes nothing — decoding a 20,000-track library would take
// minutes — so it only stats files and reads their names.
func (ix *Index) Scan(ctx context.Context, roots []string,
	onProgress func(done, total int), onTrack func(Track)) error {

	ix.reset()
	var errs []error
	var unreachable []string

	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		rootTracks, err := scanRoot(ctx, root, ix, onTrack)
		if err != nil {
			errs = append(errs, err)
			// Spec 8: an unreadable root is reported as unreachable, with the
			// reason. A silently skipped root shows up as an empty library,
			// which is indistinguishable from having no music.
			unreachable = append(unreachable, root+": "+err.Error())
		}
		if onProgress != nil {
			onProgress(ix.Len(), ix.Len())
		}
		_ = rootTracks
	}
	ix.SetUnreachable(unreachable)
	return errors.Join(errs...)
}

// scanRoot walks one root and adds its tracks to the index.
func scanRoot(ctx context.Context, root string, ix *Index, onTrack func(Track)) ([]Track, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		// A single file is a valid root: the user may pass one track.
		if t, ok := trackFor(root); ok {
			if ix.add(t) && onTrack != nil {
				onTrack(t)
			}
			return []Track{t}, nil
		}
		return nil, nil
	}

	var found []Track
	var walkErr error
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries rather than failing the whole walk.
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		t, ok := trackFor(path)
		if !ok {
			return nil
		}
		found = append(found, t)
		if ix.add(t) && onTrack != nil {
			onTrack(t)
		}
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		walkErr = err
	}
	return found, walkErr
}

// trackFor builds a Track for a path, or reports false when it is not audio.
//
// Only the filename is parsed; nothing is read from disk beyond a stat. Title
// and artist come from the filename now and are refined when the track is
// actually decoded and loaded.
func trackFor(path string) (Track, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if !audioExts[ext] {
		return Track{}, false
	}
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))

	title, artist := splitArtistTitle(stem)
	added := time.Time{}
	if fi, err := os.Stat(path); err == nil {
		added = fi.ModTime()
	}
	return Track{Path: path, Title: title, Artist: artist, Added: added}, true
}

// splitArtistTitle guesses "Artist - Title" from a filename. It is a
// placeholder until the file is decoded, which is when real metadata arrives.
func splitArtistTitle(stem string) (title, artist string) {
	if i := strings.Index(stem, " - "); i > 0 {
		return strings.TrimSpace(stem[i+3:]), strings.TrimSpace(stem[:i])
	}
	return strings.TrimSpace(stem), ""
}
