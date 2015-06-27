package wbrules

import (
	wbgo "github.com/contactless/wbgo"
	"gopkg.in/fsnotify.v1"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

type loaderOpType int

const (
	RELOAD_DELAY            = 1000 * time.Millisecond
	LOADER_OP_LIST_CAPACITY = 128
	LOADER_OP_CHANGE        = loaderOpType(iota)
	LOADER_OP_REMOVE
)

type LoaderClient interface {
	LoadScript(path string) error
	LiveLoadScript(path string) error
	LiveRemoveScript(path string) error
}

type loaderScript struct {
	explicit bool
}

type loaderOp struct {
	typ  loaderOpType
	path string
}

type Loader struct {
	initMtx   sync.Mutex
	rx        *regexp.Regexp
	client    LoaderClient
	watcher   *fsnotify.Watcher
	started   bool
	quit      chan struct{}
	delay     time.Duration
	loaded    map[string]*loaderScript
	opsByPath map[string]*loaderOp
	opList    []*loaderOp
	timer     *time.Timer
	c         <-chan time.Time
}

func NewLoader(pattern string, client LoaderClient) *Loader {
	if rx, err := regexp.Compile(pattern); err != nil {
		log.Panicf("invalid loader regexp: %s", pattern)
		return nil
	} else {
		return &Loader{
			rx:      rx,
			client:  client,
			watcher: nil,
			started: false,
			quit:    make(chan struct{}),
			delay:   RELOAD_DELAY,
			loaded:  make(map[string]*loaderScript),
		}
	}
}

func (loader *Loader) SetDelay(delay time.Duration) {
	loader.delay = delay
}

func (loader *Loader) resetOps() {
	loader.opsByPath = make(map[string]*loaderOp)
	loader.opList = make([]*loaderOp, 0, LOADER_OP_LIST_CAPACITY)
}

func (loader *Loader) registerFSEvent(ev fsnotify.Event) {
	opType := LOADER_OP_CHANGE
	if ev.Op == fsnotify.Remove || ev.Op == fsnotify.Rename {
		opType = LOADER_OP_REMOVE
	}
	op, found := loader.opsByPath[ev.Name]
	if found {
		op.typ = opType
	} else {
		op = &loaderOp{opType, ev.Name}
		loader.opsByPath[ev.Name] = op
		loader.opList = append(loader.opList, op)
	}

	if loader.timer != nil {
		loader.timer.Reset(loader.delay)
	} else {
		loader.timer = time.NewTimer(loader.delay)
		loader.c = loader.timer.C
	}
}

func (loader *Loader) processEvents() {
	for _, op := range loader.opList {
		switch op.typ {
		case LOADER_OP_CHANGE:
			wbgo.Debug.Printf("(re)load: %s", op.path)
			// need to check whether the file that possibly doesn't
			// satisfy the loader pattern was explicitly loaded
			explicit := false
			if entry, found := loader.loaded[op.path]; found {
				explicit = entry.explicit
			}
			if err := loader.doLoad(op.path, explicit, true); err != nil {
				wbgo.Warn.Printf(
					"warning: failed to load %s: %s", op.path, err)
			}
		case LOADER_OP_REMOVE:
			wbgo.Debug.Printf("file removed: %s", op.path)
			loader.removePath(op.path)
		default:
			log.Panicf("invalid loader op %d", op.typ)
		}
	}
	loader.resetOps()
}

func (loader *Loader) startWatching() {
	loader.initMtx.Lock()
	defer loader.initMtx.Unlock()
	if loader.started {
		return
	}

	var err error
	if loader.watcher, err = fsnotify.NewWatcher(); err != nil {
		wbgo.Warn.Printf("failed to create filesystem watcher: %s", err)
		return
	}

	loader.started = true
	loader.resetOps()
	go func() {
		for {
			select {
			case ev := <-loader.watcher.Events:
				wbgo.Debug.Printf("fs change event: %s", ev)
				loader.registerFSEvent(ev)
			case err := <-loader.watcher.Errors:
				wbgo.Error.Printf("watcher error: %s", err)
			case <-loader.c:
				wbgo.Debug.Printf("reload timer fired")
				loader.processEvents()
			case <-loader.quit:
				return
			}
		}
	}()
}

func (loader *Loader) shouldLoadFile(fileName string) bool {
	return loader.rx.MatchString(fileName)
}

func (loader *Loader) loadDir(filePath string, reloaded bool) error {
	wbgo.Debug.Printf("loadDir: %s", filePath)
	entries, err := ioutil.ReadDir(filePath)
	if err != nil {
		return err
	}
	if err = loader.watcher.Add(filePath); err != nil {
		wbgo.Debug.Printf("loadDir: failed to watch %s: %s", filePath, err)
	}
	for _, fi := range entries {
		fullPath := filepath.Join(filePath, fi.Name())
		wbgo.Debug.Printf("loadDir: entry: %s", fullPath)
		var err error
		switch {
		case fi.IsDir():
			err = loader.loadDir(fullPath, reloaded)
		case loader.shouldLoadFile(fi.Name()):
			err = loader.loadFile(fullPath, reloaded, false)
		}
		if err != nil {
			wbgo.Warn.Printf("couldn't load %s: %s", fullPath, err)
		}
	}
	return nil
}

func (loader *Loader) loadFile(filePath string, reloaded, explicit bool) error {
	loader.loaded[filePath] = &loaderScript{explicit}
	if reloaded {
		wbgo.Info.Printf("reloading file: %s", filePath)
		return loader.client.LiveLoadScript(filePath)
	} else {
		return loader.client.LoadScript(filePath)
	}
}

func (loader *Loader) doLoad(filePath string, explicit bool, reloaded bool) error {
	loader.startWatching()
	fi, err := os.Stat(filePath)
	switch {
	case err != nil:
		return err
	case fi.IsDir():
		return loader.loadDir(filePath, reloaded)
	case explicit:
		loader.watcher.Add(filePath)
		fallthrough
	case loader.shouldLoadFile(fi.Name()):
		return loader.loadFile(filePath, reloaded, explicit)
	default:
		wbgo.Debug.Printf("skipping loading of non-matching file %s", filePath)
		return nil
	}
}

func (loader *Loader) removePath(filePath string) {
	if loader.loaded[filePath] != nil {
		loader.client.LiveRemoveScript(filePath)
		delete(loader.loaded, filePath)
		return
	}
	// assume it's directory removal and try to find subpaths
	pathList := make([]string, 0)
	for p, _ := range loader.loaded {
		if wbgo.IsSubpath(filePath, p) {
			pathList = append(pathList, p)
		}
	}
	sort.Strings(pathList)
	for _, p := range pathList {
		loader.removePath(p)
	}
}

func (loader *Loader) Load(filePath string) error {
	return loader.doLoad(filePath, true, false)
}

func (loader *Loader) Stop() {
	loader.initMtx.Lock()
	defer loader.initMtx.Unlock()
	if !loader.started {
		return
	}
	loader.quit <- struct{}{}
	loader.watcher.Close()
	loader.started = false
}
