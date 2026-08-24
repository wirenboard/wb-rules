package wbrules

// fs.watch for the built-in fs module: ONE inotify instance per engine
// (like libuv's per-loop instance) shared by every watcher, read by one
// goroutine, events delivered to the JS listeners on the engine loop.
// Implemented directly over inotify(7) through x/sys/unix (already a
// dependency) rather than a watcher library, so the wbgo.so plugin's
// dependency set stays untouched.
//
// Why one instance: inotify instances are a scarce per-user resource
// (fs.inotify.max_user_instances, typically 128, shared with every other
// root daemon on the controller), watches are plentiful
// (max_user_watches, thousands). A watcher costs one watch descriptor;
// watchers of the same inode share the descriptor (the kernel returns the
// same wd, and every watcher subscribes with the same mask).
//
// Lifecycle: a watcher belongs to the realm (rule file) that created it
// and is tracked per context like timers are (ctxTimers): runCleanups
// closes the file's watchers on reload/removal on the engine loop, Stop
// closes every watcher after the loops are gone, the kernel dropping a
// watch (the watched path deleted or its filesystem unmounted, reported
// as IN_IGNORED) closes the watchers on it. Closing from JS
// (FSWatcher.close) and every other path meet in close(), which is
// idempotent; the listener's stash entry is swept on the engine loop.
// No per-watcher entry goes on the file's cleanup list: with -cleanup the
// list runs from the Stop caller's goroutine while JS may still execute,
// and a heap-touching entry there would race the interpreter. The
// instance itself is opened on the first watcher and released when the
// last one goes.

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
	wd       int32
	inst     *os.File // the instance the wd belongs to

	closed    atomic.Bool
	closeOnce sync.Once
}

// fsWatchState is the engine's shared inotify instance and its watcher
// tables; all fields are guarded by mu except file, which is replaced
// only under mu and read by the reader goroutine that owns it.
type fsWatchState struct {
	mu   sync.Mutex
	file *os.File // nil while no watcher exists
	// fd is file's descriptor kept apart: (*os.File).Fd() would switch the
	// descriptor to blocking mode and detach it from the runtime poller,
	// after which Close no longer wakes the reader's pending Read
	fd       int
	seq      uint64
	byID     map[uint64]*fsWatcher
	byWd     map[int32][]*fsWatcher
	byCtx    map[*ESContext]map[uint64]*fsWatcher // per realm, for runCleanups
	watchers int                                  // == len(byID)
}

// _wbFsWatch(path, listener) -> watcher id. The listener receives
// {eventType, filename} objects; the JS layer spreads them into Node's
// (eventType, filename) signature.
func (engine *ESEngine) esWbFsWatch(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		return duktape.DUK_RET_TYPE_ERROR
	}
	path := ctx.GetString(0)
	st := &engine.fsWatch
	st.mu.Lock()
	if st.file == nil {
		fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
		if err != nil {
			st.mu.Unlock()
			return throwFsError(ctx, fsErrFromGo(err, "watch", path))
		}
		// a non-blocking descriptor registered with the runtime poller:
		// Read parks in the netpoller and Close wakes it, which is what
		// lets the last close() stop the reader without a side channel
		st.file = os.NewFile(uintptr(fd), "inotify")
		st.fd = fd
		st.byID = make(map[uint64]*fsWatcher)
		st.byWd = make(map[int32][]*fsWatcher)
		if st.byCtx == nil {
			st.byCtx = make(map[*ESContext]map[uint64]*fsWatcher)
		}
		go engine.fsWatchLoop(st.file)
	}
	wd, err := unix.InotifyAddWatch(st.fd, path, fsWatchMask)
	if err != nil {
		if st.watchers == 0 {
			// nothing else uses the instance: give it back right away
			st.file.Close()
			st.file = nil
			st.fd = -1
		}
		st.mu.Unlock()
		return throwFsError(ctx, fsErrFromGo(err, "watch", path))
	}
	st.seq++
	w := &fsWatcher{
		id:       st.seq,
		engine:   engine,
		ctx:      ctx,
		key:      ctx.storeCallback(1),
		path:     path,
		baseName: filepath.Base(path),
		wd:       int32(wd),
		inst:     st.file,
	}
	st.byID[w.id] = w
	st.byWd[w.wd] = append(st.byWd[w.wd], w)
	if st.byCtx[ctx] == nil {
		st.byCtx[ctx] = make(map[uint64]*fsWatcher)
	}
	st.byCtx[ctx][w.id] = w
	st.watchers++
	st.mu.Unlock()

	ctx.PushNumber(float64(w.id))
	return 1
}

// runFsWatchCleanups closes the watchers of a realm that is being torn
// down (reload/removal); runs on the engine loop from runCleanups, where
// sweeping the stash entries is safe (invalidate() would sweep them too,
// but the kernel watches must go now).
func (engine *ESEngine) runFsWatchCleanups(ctx *ESContext) {
	st := &engine.fsWatch
	st.mu.Lock()
	watchers := make([]*fsWatcher, 0, len(st.byCtx[ctx]))
	for _, w := range st.byCtx[ctx] {
		watchers = append(watchers, w)
	}
	st.mu.Unlock()
	for _, w := range watchers {
		w.close(true)
	}
}

// _wbFsWatchClose(id): FSWatcher.close(); unknown ids (already closed)
// are ignored like Node's repeated close().
func (engine *ESEngine) esWbFsWatchClose(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsNumber(0) {
		return duktape.DUK_RET_TYPE_ERROR
	}
	id := uint64(ctx.GetNumber(0))
	st := &engine.fsWatch
	st.mu.Lock()
	w := st.byID[id]
	st.mu.Unlock()
	if w != nil {
		w.close(true)
	}
	return 0
}

// closeFsWatchers closes every watcher; called at Stop, after the loops
// are gone (single-threaded, so sweeping stash entries is safe).
func (engine *ESEngine) closeFsWatchers() {
	for _, w := range engine.fsWatch.snapshot() {
		w.close(true)
	}
}

// FsWatcherCount reports live watchers (tests: cleanup and Stop sweep).
func (engine *ESEngine) FsWatcherCount() int {
	engine.fsWatch.mu.Lock()
	defer engine.fsWatch.mu.Unlock()
	return engine.fsWatch.watchers
}

// FsWatchInstanceOpen reports whether the shared inotify instance is
// currently held (tests: it must be released with the last watcher).
func (engine *ESEngine) FsWatchInstanceOpen() bool {
	engine.fsWatch.mu.Lock()
	defer engine.fsWatch.mu.Unlock()
	return engine.fsWatch.file != nil
}

func (st *fsWatchState) snapshot() []*fsWatcher {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]*fsWatcher, 0, len(st.byID))
	for _, w := range st.byID {
		out = append(out, w)
	}
	return out
}

// forWd returns the watchers subscribed to a watch descriptor.
func (st *fsWatchState) forWd(wd int32) []*fsWatcher {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]*fsWatcher(nil), st.byWd[wd]...)
}

// detach removes the watcher from the tables, drops the kernel watch when
// it was the last subscriber of its descriptor, and releases the inotify
// instance when it was the last watcher altogether.
func (st *fsWatchState) detach(w *fsWatcher) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.byID[w.id]; !ok {
		return
	}
	delete(st.byID, w.id)
	st.watchers--
	if perCtx := st.byCtx[w.ctx]; perCtx != nil {
		delete(perCtx, w.id)
		if len(perCtx) == 0 {
			delete(st.byCtx, w.ctx)
		}
	}
	rest := st.byWd[w.wd][:0]
	for _, o := range st.byWd[w.wd] {
		if o != w {
			rest = append(rest, o)
		}
	}
	if len(rest) > 0 {
		st.byWd[w.wd] = rest
		return
	}
	delete(st.byWd, w.wd)
	if st.file == nil || st.file != w.inst {
		// the instance is gone (or already replaced): nothing to remove
		return
	}
	if st.watchers == 0 {
		// the reader goroutine wakes from Read with an error and exits;
		// the kernel watches die with the descriptor. Closing an inotify
		// descriptor costs milliseconds (an RCU grace period in the
		// kernel), so it happens off the engine loop.
		file := st.file
		st.file = nil
		st.fd = -1
		go file.Close()
		return
	}
	// EINVAL when the kernel already removed it (IN_IGNORED): harmless
	unix.InotifyRmWatch(st.fd, uint32(w.wd))
}

// close stops the watcher. sweep must be true only on the engine loop (or
// with the loops stopped): it removes the listener's stash entry, which
// touches the JS heap. Off-loop callers pass false and hand the sweep to
// the next single-threaded boundary via noteOrphanedCallback.
func (w *fsWatcher) close(sweep bool) {
	w.closeOnce.Do(func() {
		w.closed.Store(true)
		w.engine.fsWatch.detach(w)
		if sweep {
			w.ctx.RemoveCallback(w.key)
		} else {
			w.engine.noteOrphanedCallback(w.ctx, w.key)
		}
	})
}

// closeFromReader closes a watcher on the reader goroutine's behalf: the
// stash sweep must happen on the loop, so it is scheduled there. The
// orphan note is taken before the thunk is queued: a stopping engine may
// accept the thunk and then drop it in its queue drain, and the note is
// what gets the entry swept at the next single-threaded boundary (the
// thunk itself withdraws it when it does run).
func (w *fsWatcher) closeFromReader() {
	if w.closed.Load() {
		return
	}
	w.engine.noteOrphanedCallback(w.ctx, w.key)
	if err := w.engine.MaybeCallSync(func() {
		w.engine.forgetOrphanedCallback(w.ctx, w.key)
		w.close(true)
	}); err != nil {
		w.close(false)
	}
}

// fsWatchLoop reads the shared instance until its descriptor is closed
// (detach closes it with the last watcher).
func (engine *ESEngine) fsWatchLoop(file *os.File) {
	buf := make([]byte, 64*1024)
	for {
		n, err := file.Read(buf)
		if err != nil {
			// closed by detach with the last watcher (nothing left to do),
			// or a fatal descriptor error: then the instance is over and
			// the watchers still pointing at it are closed
			engine.fsWatch.mu.Lock()
			fatal := engine.fsWatch.file == file
			if fatal {
				engine.fsWatch.file = nil
				engine.fsWatch.fd = -1
			}
			engine.fsWatch.mu.Unlock()
			if fatal {
				for _, w := range engine.fsWatch.snapshot() {
					if w.inst == file {
						w.closeFromReader()
					}
				}
			}
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
			if ev.Mask&unix.IN_Q_OVERFLOW != 0 {
				engine.fsLogWarning("fs.watch: inotify event queue overflowed, events were lost")
				continue
			}
			watchers := engine.fsWatch.forWd(ev.Wd)
			if ev.Mask&unix.IN_IGNORED != 0 {
				// the kernel removed the watch: the path was deleted or its
				// filesystem unmounted. Node's watcher goes silent forever
				// here; ours frees its resources.
				for _, w := range watchers {
					w.closeFromReader()
				}
				continue
			}
			eventType := "change"
			if ev.Mask&fsWatchRenameMask != 0 {
				eventType = "rename"
			}
			for _, w := range watchers {
				filename := name
				if filename == "" {
					filename = w.baseName
				}
				w.deliver(eventType, filename)
			}
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
