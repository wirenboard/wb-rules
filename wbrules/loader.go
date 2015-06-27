package wbrules

import (
	wbgo "github.com/contactless/wbgo"
	"gopkg.in/fsnotify.v1"
	"io/ioutil"
	"log"
	"os"
	"path"
	"regexp"
	"sync"
	"time"
)

const (
	RELOAD_DELAY       = 1000 * time.Millisecond
	PATH_LIST_CAPACITY = 128
)

type LoaderClient interface {
	LoadScript(path string) error
	LiveLoadScript(path string) error
}

type LoadFunc func(string, bool) error

type Loader struct {
	initMtx          sync.Mutex
	rx               *regexp.Regexp
	client           LoaderClient
	watcher          *fsnotify.Watcher
	started          bool
	quit             chan struct{}
	delay            time.Duration
	explicitlyLoaded map[string]bool
}

func NewLoader(pattern string, client LoaderClient) *Loader {
	if rx, err := regexp.Compile(pattern); err != nil {
		log.Panicf("invalid loader regexp: %s", pattern)
		return nil
	} else {
		return &Loader{
			rx:               rx,
			client:           client,
			watcher:          nil,
			started:          false,
			quit:             make(chan struct{}),
			delay:            RELOAD_DELAY,
			explicitlyLoaded: make(map[string]bool),
		}
	}
}

func (loader *Loader) SetDelay(delay time.Duration) {
	loader.delay = delay
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
	reloadPaths := make(map[string]int)
	pathList := make([]string, 0, PATH_LIST_CAPACITY)
	var timer *time.Timer
	var c <-chan time.Time
	go func() {
		for {
			select {
			case ev := <-loader.watcher.Events:
				wbgo.Debug.Printf("fs change event: %s", ev)
				if ev.Op == fsnotify.Remove || ev.Op == fsnotify.Rename {
					// should not try to load files that no longer exist
					if pos, found := reloadPaths[ev.Name]; found {
						pathList = append(pathList[:pos], pathList[pos+1:]...)
						delete(reloadPaths, ev.Name)
					}
					break
				}
				// preserve chronological order of the
				// filesystem change events
				if _, found := reloadPaths[ev.Name]; !found {
					reloadPaths[ev.Name] = len(pathList)
					pathList = append(pathList, ev.Name)
				}

				if timer != nil {
					timer.Reset(loader.delay)
				} else {
					timer = time.NewTimer(loader.delay)
					c = timer.C
				}
			case err := <-loader.watcher.Errors:
				wbgo.Error.Printf("watcher error: %s", err)
			case <-c:
				wbgo.Debug.Printf("reload timer fired")
				for _, p := range pathList {
					wbgo.Debug.Printf("(re)load: %s", p)
					// need to check whether the file that possibly doesn't
					// satisfy the loader pattern was explicitly loaded
					explicit := loader.explicitlyLoaded[p]
					if err := loader.doLoad(p, explicit, true); err != nil {
						wbgo.Warn.Printf(
							"warning: failed to load %s: %s", p, err)
					}
				}
				reloadPaths = make(map[string]int)
				pathList = pathList[:0]
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
		fullPath := path.Join(filePath, fi.Name())
		wbgo.Debug.Printf("loadDir: entry: %s", fullPath)
		var err error
		switch {
		case fi.IsDir():
			err = loader.loadDir(fullPath, reloaded)
		case loader.shouldLoadFile(fi.Name()):
			err = loader.loadFile(fullPath, reloaded)
		}
		if err != nil {
			wbgo.Warn.Printf("couldn't load %s: %s", fullPath, err)
		}
	}
	return nil
}

func (loader *Loader) loadFile(filePath string, reloaded bool) error {
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
		loader.explicitlyLoaded[filePath] = true
		loader.watcher.Add(filePath)
		return loader.loadFile(filePath, reloaded)
	case loader.shouldLoadFile(fi.Name()):
		return loader.loadFile(filePath, reloaded)
	default:
		wbgo.Debug.Printf("skipping loading of non-matching file %s", filePath)
		return nil
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
