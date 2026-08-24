package wbrules

// fs.watch for the built-in fs module: one inotify instance per watcher,
// read by its own goroutine, events delivered to the JS listener on the
// engine loop. Implemented directly over inotify(7) through x/sys/unix
// (already a dependency) rather than a watcher library, so the wbgo.so
// plugin's dependency set stays untouched.
//
// Lifecycle: a watcher belongs to the rule file that created it - the
// file's cleanup scope closes it on reload/removal; Stop closes every
// watcher; the kernel dropping the watch (the watched path deleted or its
// filesystem unmounted, reported as IN_IGNORED) closes it too. Closing
// from JS (FSWatcher.close) and every other path meet in close(), which
// is idempotent; the listener's stash entry is swept on the engine loop.

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/stretchr/objx"
	duktape "github.com/wirenboard/go-duktape"
	"github.com/wirenboard/wbgong"
	"golang.org/x/sys/unix"
)

// fsWatchMask mirrors libuv's inotify subscription so event counts match
// Node's (one 'change' per IN_MODIFY, no IN_CLOSE_WRITE duplicates).
const fsWatchMask = unix.IN_ATTRIB | unix.IN_CREATE | unix.IN_MODIFY | unix.IN_DELETE |
	unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_MOVED_FROM | unix.IN_MOVED_TO

const fsWatchRenameMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_DELETE_SELF |
	unix.IN_MOVE_SELF | unix.IN_MOVED_FROM | unix.IN_MOVED_TO

type fsWatcher struct {
	id       uint64
	engine   *ESEngine
	ctx      *ESContext
	key      ESCallback
	path     string
	baseName string
	file     *os.File // the inotify descriptor, pollable through the runtime

	closed    atomic.Bool
	closeOnce sync.Once
}

// _wbFsWatch(path, listener) -> watcher id. The listener receives
// {eventType, filename} objects; the JS layer spreads them into Node's
// (eventType, filename) signature.
func (engine *ESEngine) esWbFsWatch(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		return duktape.DUK_RET_TYPE_ERROR
	}
	path := ctx.GetString(0)
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return throwFsError(ctx, fsErrFromGo(err, "watch", path))
	}
	if _, err := unix.InotifyAddWatch(fd, path, fsWatchMask); err != nil {
		unix.Close(fd)
		return throwFsError(ctx, fsErrFromGo(err, "watch", path))
	}
	w := &fsWatcher{
		engine:   engine,
		ctx:      ctx,
		key:      ctx.storeCallback(1),
		path:     path,
		baseName: filepath.Base(path),
		// a non-blocking descriptor registered with the runtime poller:
		// Read parks in the netpoller and Close wakes it, which is what
		// lets close() stop the goroutine without a side channel
		file: os.NewFile(uintptr(fd), "inotify:"+path),
	}
	engine.fsWatchersMtx.Lock()
	engine.fsWatcherSeq++
	w.id = engine.fsWatcherSeq
	engine.fsWatchers[w.id] = w
	engine.fsWatchersMtx.Unlock()

	// the watcher dies with its file: the cleanup runs on the engine loop
	// during reload/removal, where sweeping the stash entry is safe
	if currentFilename := ctx.GetCurrentFilename(); currentFilename != "" {
		engine.cleanup.PushCleanupScope(currentFilename)
		defer engine.cleanup.PopCleanupScope(currentFilename)
	}
	engine.cleanup.AddCleanup(func() { w.close(true) })

	go w.loop()
	ctx.PushNumber(float64(w.id))
	return 1
}

// _wbFsWatchClose(id): FSWatcher.close(); unknown ids (already closed)
// are ignored like Node's repeated close().
func (engine *ESEngine) esWbFsWatchClose(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsNumber(0) {
		return duktape.DUK_RET_TYPE_ERROR
	}
	id := uint64(ctx.GetNumber(0))
	engine.fsWatchersMtx.Lock()
	w := engine.fsWatchers[id]
	engine.fsWatchersMtx.Unlock()
	if w != nil {
		w.close(true)
	}
	return 0
}

// closeFsWatchers closes every watcher; called at Stop, after the loops
// are gone (single-threaded, so sweeping stash entries is safe).
func (engine *ESEngine) closeFsWatchers() {
	engine.fsWatchersMtx.Lock()
	watchers := make([]*fsWatcher, 0, len(engine.fsWatchers))
	for _, w := range engine.fsWatchers {
		watchers = append(watchers, w)
	}
	engine.fsWatchersMtx.Unlock()
	for _, w := range watchers {
		w.close(true)
	}
}

// FsWatcherCount reports live watchers (tests: cleanup and Stop sweep).
func (engine *ESEngine) FsWatcherCount() int {
	engine.fsWatchersMtx.Lock()
	defer engine.fsWatchersMtx.Unlock()
	return len(engine.fsWatchers)
}

// close stops the watcher. sweep must be true only on the engine loop (or
// with the loops stopped): it removes the listener's stash entry, which
// touches the JS heap. Off-loop callers pass false and hand the sweep to
// the next single-threaded boundary via noteOrphanedCallback.
func (w *fsWatcher) close(sweep bool) {
	w.closeOnce.Do(func() {
		w.closed.Store(true)
		w.file.Close()
		w.engine.fsWatchersMtx.Lock()
		delete(w.engine.fsWatchers, w.id)
		w.engine.fsWatchersMtx.Unlock()
		if sweep {
			w.ctx.RemoveCallback(w.key)
		} else {
			w.engine.noteOrphanedCallback(w.ctx, w.key)
		}
	})
}

// finish is the reader goroutine's exit path: the sweep must happen on the
// loop, so it is scheduled there; a stopped engine sweeps at its next
// boundary instead.
func (w *fsWatcher) finish() {
	if w.closed.Load() {
		return
	}
	if err := w.engine.MaybeCallSync(func() { w.close(true) }); err != nil {
		w.close(false)
	}
}

func (w *fsWatcher) loop() {
	defer w.finish()
	buf := make([]byte, 64*1024)
	for {
		n, err := w.file.Read(buf)
		if err != nil {
			// closed (by close()) or a fatal descriptor error: either way
			// the watcher is over
			return
		}
		off := 0
		for off+unix.SizeofInotifyEvent <= n {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			nameLen := int(ev.Len)
			name := ""
			if nameLen > 0 {
				raw := buf[off+unix.SizeofInotifyEvent : off+unix.SizeofInotifyEvent+nameLen]
				for i, b := range raw {
					if b == 0 {
						raw = raw[:i]
						break
					}
				}
				name = string(raw)
			}
			off += unix.SizeofInotifyEvent + nameLen
			switch {
			case ev.Mask&unix.IN_Q_OVERFLOW != 0:
				w.engine.fsLogWarning("fs.watch(%s): inotify event queue overflowed, events were lost", w.path)
				continue
			case ev.Mask&unix.IN_IGNORED != 0:
				// the kernel removed the watch: the path was deleted or its
				// filesystem unmounted. Node's watcher goes silent forever
				// here; ours frees its resources.
				return
			}
			eventType := "change"
			if ev.Mask&fsWatchRenameMask != 0 {
				eventType = "rename"
			}
			filename := name
			if filename == "" {
				filename = w.baseName
			}
			w.deliver(eventType, filename)
		}
	}
}

// deliver hands one event to the listener on the engine loop; a stopped
// engine drops it (the watcher is being closed by Stop anyway).
func (w *fsWatcher) deliver(eventType, filename string) {
	if w.closed.Load() {
		return
	}
	_ = w.engine.MaybeCallSync(func() {
		if w.closed.Load() || !w.ctx.IsValid() {
			return
		}
		if currentFilename := w.ctx.GetCurrentFilename(); currentFilename != "" {
			w.engine.cleanup.PushCleanupScope(currentFilename)
			defer w.engine.cleanup.PopCleanupScope(currentFilename)
		}
		wbgong.Debug.Printf("fs.watch(%s): %s %s", w.path, eventType, filename)
		w.ctx.invokeCallback(w.key, objx.New(map[string]any{
			"eventType": eventType,
			"filename":  filename,
		}))
	})
}
