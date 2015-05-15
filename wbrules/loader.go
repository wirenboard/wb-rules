package wbrules

import (
	wbgo "github.com/contactless/wbgo"
	"io/ioutil"
	"log"
	"path"
	"regexp"
)

type LoadFunc func(string) error

type Loader struct {
	rx       *regexp.Regexp
	loadFunc LoadFunc
}

func NewLoader(pattern string, loadFunc LoadFunc) *Loader {
	if rx, err := regexp.Compile(pattern); err != nil {
		log.Panicf("invalid loader regexp: %s", pattern)
		return nil
	} else {
		return &Loader{rx, loadFunc}
	}
}

func (loader *Loader) loadDir(filePath string) error {
	wbgo.Debug.Printf("loadDir: %s", filePath)
	entries, err := ioutil.ReadDir(filePath)
	if err != nil {
		return err
	}
	for _, fi := range entries {
		fullPath := path.Join(filePath, fi.Name())
		wbgo.Debug.Printf("loadDir: entry: %s", fullPath)
		var err error
		switch {
		case fi.IsDir():
			err = loader.loadDir(fullPath)
		case loader.rx.MatchString(fi.Name()):
			err = loader.loadFile(fullPath)
		}
		if err != nil {
			wbgo.Warn.Printf("couldn't load %s: %s", fullPath, err)
		}
	}
	return nil
}

func (loader *Loader) loadFile(filePath string) error {
	return loader.loadFunc(filePath)
}

func (loader *Loader) Load(filePath string) error {
	isDir, err := wbgo.IsDirPath(filePath)
	switch {
	case err != nil:
		return err
	case isDir:
		return loader.loadDir(filePath)
	default:
		return loader.loadFile(filePath)
	}
}
