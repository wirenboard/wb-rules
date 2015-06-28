package wbrules

import (
	"github.com/contactless/wbgo"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var editorPathRx = regexp.MustCompile(`^[\w/-]{0,256}\w{1,253}\.js$`)

type Editor struct {
	locFileManager LocFileManager
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
	// no iota here because these values may be used
	// by external software
	EDITOR_ERROR_INVALID_PATH   = 1000
	EDITOR_ERROR_LISTDIR        = 1001
	EDITOR_ERROR_WRITE          = 1002
	EDITOR_ERROR_FILE_NOT_FOUND = 1003
	EDITOR_ERROR_REMOVE         = 1004
)

var invalidPathError = &EditorError{EDITOR_ERROR_INVALID_PATH, "Invalid path"}
var listDirError = &EditorError{EDITOR_ERROR_LISTDIR, "Error listing the directory"}
var writeError = &EditorError{EDITOR_ERROR_WRITE, "Error writing the file"}
var fileNotFoundError = &EditorError{EDITOR_ERROR_FILE_NOT_FOUND, "File not found"}
var rmError = &EditorError{EDITOR_ERROR_REMOVE, "Error removing the file"}

func NewEditor(locFileManager LocFileManager) *Editor {
	return &Editor{locFileManager}
}

func (editor *Editor) List(args *struct{}, reply *[]LocFileEntry) (err error) {
	*reply, err = editor.locFileManager.ListSourceFiles()
	return
}

type EditorSaveArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (editor *Editor) Save(args *EditorSaveArgs, reply *bool) error {
	if !editorPathRx.MatchString(args.Path) {
		return invalidPathError
	}

	targetPath := filepath.Join(editor.locFileManager.ScriptDir(), args.Path)

	if strings.Contains(args.Path, "/") {
		if err := os.MkdirAll(filepath.Dir(targetPath), 0777); err != nil {
			wbgo.Error.Printf("error making dirs for %s: %s", targetPath, err)
			return writeError
		}
	}

	if err := ioutil.WriteFile(targetPath, []byte(args.Content), 0777); err != nil {
		wbgo.Error.Printf("error writing %s: %s", targetPath, err)
		return writeError
	}

	*reply = true
	return nil
}

type EditorRemoveArgs struct {
	Path string `json:"path"`
}

func (editor *Editor) Remove(args *EditorRemoveArgs, reply *bool) error {
	entries, err := editor.locFileManager.ListSourceFiles()
	if err != nil {
		// yes, listing the directory is necessary to check whether
		// the file can be removed
		return listDirError
	}

	for _, entry := range entries {
		if entry.VirtualPath != args.Path {
			continue
		}
		if err := os.Remove(entry.PhysicalPath); err != nil {
			wbgo.Error.Printf("error removing %s: %s", entry.PhysicalPath, err)
			return rmError
		}
		*reply = true
		return nil
	}

	return fileNotFoundError
}
