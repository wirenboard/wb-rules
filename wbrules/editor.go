package wbrules

import (
	"io/ioutil"
	"os"
	"path"
	"strings"
)

type Editor struct {
	rootDir string
}

type EditorError struct {
	code    int32
	message string
}

func (err *EditorError) Error() string {
	return err.message
}

func (err *EditorError) ErrorCode() int32 {
	return err.code
}

const (
	// TBD: rm iota here
	EDITOR_ERROR_INVALID_PATH = 1000 - iota
	EDITOR_ERROR_NODIR
	EDITOR_ERROR_LISTDIR
)

var invalidPathError = &EditorError{EDITOR_ERROR_INVALID_PATH, "Invalid path"}
var directoryNotFoundError = &EditorError{EDITOR_ERROR_NODIR, "Directory not found"}
var readDirError = &EditorError{EDITOR_ERROR_LISTDIR, "Error listing the directory"}

func NewEditor(rootDir string) *Editor {
	return &Editor{rootDir}
}

type PathArgs struct {
	Path string `json:"path"`
}

func (editor *Editor) List(args *PathArgs, reply *[]string) error {
	dirPath := path.Clean(args.Path)
	// exclude .. and hidden dirs in toplevel directory
	// (fixme: include hidden dirs in subdirs, too)
	if strings.HasPrefix(dirPath, ".") {
		return invalidPathError
	}
	var fullPath string
	if strings.HasPrefix(dirPath, "/") {
		dirPath = dirPath[1:]
	}
	if dirPath == "" {
		fullPath = editor.rootDir
	} else {
		fullPath = path.Join(editor.rootDir, dirPath)
	}

	fi, err := os.Stat(fullPath)
	switch {
	case err != nil:
		fallthrough
	case !fi.IsDir():
		return directoryNotFoundError
	}

	entries, err := ioutil.ReadDir(fullPath)
	if err != nil {
		return readDirError
	}

	*reply = make([]string, len(entries))
	for n, entry := range entries {
		(*reply)[n] = entry.Name()
	}

	return nil
}
