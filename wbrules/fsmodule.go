package wbrules

// The built-in "fs" module: a Node.js-shaped filesystem API for rule
// scripts. The JS surface (argument validation, Stats/Dirent classes,
// promises) lives in fsmodule.js, embedded below and served through
// ModSearch so `require("fs")` resolves to it in every realm; this file
// provides the two realm-local builtins it calls:
//
//	_wbFsSync(op, args)            runs an operation on the engine loop and
//	                               returns its result (throws a Node-style
//	                               error: code/errno/syscall/path/dest)
//	_wbFsAsync(op, args, callback) runs it on a goroutine and reports
//	                               {error} or {result} through the callback
//	                               exactly once, back on the engine loop
//
// Every operation is a plain Go function over already-validated arguments
// (fsOps), shared by both entry points. fs.watch is in fswatch.go.

import (
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/stretchr/objx"
	duktape "github.com/wirenboard/go-duktape"
	"github.com/wirenboard/wbgong"
	"golang.org/x/sys/unix"
)

//go:embed fsmodule.js
var fsModuleSource string

// fsPromisesModuleSource backs require("fs/promises"): the same functions
// the top-level module exposes, as a separate module object like Node's.
const fsPromisesModuleSource = "module.exports = require('fs').promises;\n"

// builtinModuleFilename is the filename builtin module code is compiled
// under - it shows up in tracebacks and can never collide with a file on
// disk (the module directories are searched only after this table).
func builtinModuleFilename(id string) string { return "<builtin>/" + id + ".js" }

// builtinModuleSource returns the source of a module implemented by the
// engine itself. Like Node's core modules these shadow same-named files in
// the module directories. The node: spellings re-export the plain module
// so every spelling yields the same object within a file.
func builtinModuleSource(id string) (string, bool) {
	switch id {
	case "fs":
		return fsModuleSource, true
	case "fs/promises":
		return fsPromisesModuleSource, true
	case "node:fs":
		return "module.exports = require('fs');\n", true
	case "node:fs/promises":
		return "module.exports = require('fs/promises');\n", true
	}
	return "", false
}

// fsMaxReadFileSize bounds readFile: one shared JS heap serves every rule
// file, so an accidental read of a multi-gigabyte log (or an endless
// /dev or /proc stream) must fail instead of exhausting it.
const fsMaxReadFileSize = 10 << 20

// fsError is a filesystem failure in Node's shape: code is the errno name
// ("ENOENT") or an ERR_* code for non-OS failures, errno the negative
// errno value (0 when not an OS error), syscall/path/dest as Node reports
// them, message the full "ENOENT: no such file or directory, open '/x'".
type fsError struct {
	Code    string
	Errno   syscall.Errno
	Syscall string
	Path    string
	Dest    string
	HasPath bool
	HasDest bool
	Message string
}

func (e *fsError) Error() string { return e.Message }

// errnoCode names an errno the way Node's error.code does.
func errnoCode(errno syscall.Errno) string {
	if name := unix.ErrnoName(errno); name != "" {
		return name
	}
	return fmt.Sprintf("EUNKNOWN%d", int(errno))
}

func newFsErrno(errno syscall.Errno, syscallName, path string) *fsError {
	code := errnoCode(errno)
	return &fsError{
		Code:    code,
		Errno:   errno,
		Syscall: syscallName,
		Path:    path,
		HasPath: true,
		Message: fmt.Sprintf("%s: %s, %s '%s'", code, errno.Error(), syscallName, path),
	}
}

func newFsErrnoDest(errno syscall.Errno, syscallName, path, dest string) *fsError {
	e := newFsErrno(errno, syscallName, path)
	e.Dest = dest
	e.HasDest = true
	e.Message = fmt.Sprintf("%s: %s, %s '%s' -> '%s'", e.Code, errno.Error(), syscallName, path, dest)
	return e
}

// newFsCodeError is a non-errno failure (Node's ERR_* system errors).
func newFsCodeError(code, syscallName, path, message string) *fsError {
	return &fsError{Code: code, Syscall: syscallName, Path: path, HasPath: true, Message: message}
}

// fsErrFromGo converts an error from the os/io packages: the errno inside
// a *PathError/*LinkError (or a bare Errno) becomes a Node error under the
// given syscall name; anything else is reported as ERR_SYSTEM_ERROR.
func fsErrFromGo(err error, syscallName, path string) *fsError {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return newFsErrno(errno, syscallName, path)
	}
	return newFsCodeError("ERR_SYSTEM_ERROR", syscallName, path,
		fmt.Sprintf("%s: %s '%s'", err.Error(), syscallName, path))
}

func fsErrFromGoDest(err error, syscallName, path, dest string) *fsError {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return newFsErrnoDest(errno, syscallName, path, dest)
	}
	e := newFsCodeError("ERR_SYSTEM_ERROR", syscallName, path,
		fmt.Sprintf("%s: %s '%s' -> '%s'", err.Error(), syscallName, path, dest))
	e.Dest = dest
	e.HasDest = true
	return e
}

// descriptor is the plain-object form handed to the async callback; the
// JS side turns it into an Error with the same properties.
func (e *fsError) descriptor() map[string]any {
	d := map[string]any{
		"code":    e.Code,
		"syscall": e.Syscall,
		"message": e.Message,
	}
	if e.Errno != 0 {
		d["errno"] = -float64(e.Errno)
	}
	if e.HasPath {
		d["path"] = e.Path
	}
	if e.HasDest {
		d["dest"] = e.Dest
	}
	return d
}

// throwFsError pushes the error as a real Error of the calling realm (so
// it carries a JS stack and passes instanceof Error) with Node's
// properties, and returns the rc that throws the stack top.
func throwFsError(ctx *ESContext, e *fsError) int {
	ctx.PushErrorObject(duktape.DUK_ERR_ERROR, e.Message)
	ctx.PushString(e.Code)
	ctx.PutPropString(-2, "code")
	if e.Errno != 0 {
		ctx.PushNumber(-float64(e.Errno))
		ctx.PutPropString(-2, "errno")
	}
	ctx.PushString(e.Syscall)
	ctx.PutPropString(-2, "syscall")
	if e.HasPath {
		ctx.PushString(e.Path)
		ctx.PutPropString(-2, "path")
	}
	if e.HasDest {
		ctx.PushString(e.Dest)
		ctx.PutPropString(-2, "dest")
	}
	return duktape.DUK_RET_INSTACK_ERROR
}

// fsArgs is the argument vector of one operation, already validated and
// normalized by the JS layer; the typed getters fail closed (an
// ERR_INVALID_ARG_TYPE error) rather than panicking on a foreign caller.
type fsArgs struct {
	op   string
	args []any
	err  *fsError
}

func (a *fsArgs) bad(i int, want string) {
	if a.err == nil {
		got := "undefined"
		if i < len(a.args) && a.args[i] != nil {
			got = fmt.Sprintf("%T", a.args[i])
		}
		a.err = newFsCodeError("ERR_INVALID_ARG_TYPE", a.op, "",
			fmt.Sprintf("%s: argument %d must be of type %s, received %s", a.op, i, want, got))
		a.err.HasPath = false
	}
}

func (a *fsArgs) str(i int) string {
	if i < len(a.args) {
		if s, ok := a.args[i].(string); ok {
			return s
		}
	}
	a.bad(i, "string")
	return ""
}

func (a *fsArgs) boolean(i int) bool {
	if i < len(a.args) {
		if b, ok := a.args[i].(bool); ok {
			return b
		}
	}
	a.bad(i, "boolean")
	return false
}

func (a *fsArgs) num(i int) float64 {
	if i < len(a.args) {
		if n, ok := a.args[i].(float64); ok {
			return n
		}
	}
	a.bad(i, "number")
	return 0
}

type fsOpFunc func(a *fsArgs) (any, *fsError)

// fsOps maps operation names (the JS layer's vocabulary) to
// implementations. Results are plain Go values PushJSObject understands:
// strings, bools, numbers, nil, []any and map[string]any.
var fsOps = map[string]fsOpFunc{
	"readFile":  fsReadFile,
	"writeFile": fsWriteFile,
	"stat":      func(a *fsArgs) (any, *fsError) { return fsStat(a, true) },
	"lstat":     func(a *fsArgs) (any, *fsError) { return fsStat(a, false) },
	"readdir":   fsReaddir,
	"exists":    fsExists,
	"mkdir":     fsMkdir,
	"rmdir":     fsRmdir,
	"rm":        fsRm,
	"unlink":    fsUnlink,
	"rename":    fsRename,
	"copyFile":  fsCopyFile,
	"access":    fsAccess,
	"realpath":  fsRealpath,
	"readlink":  fsReadlink,
	"symlink":   fsSymlink,
	"chmod":     fsChmod,
	"mkdtemp":   fsMkdtemp,
	"truncate":  fsTruncate,
	"utimes":    fsUtimes,
}

func runFsOp(op string, args []any) (any, *fsError) {
	fn, ok := fsOps[op]
	if !ok {
		return nil, newFsCodeError("ERR_INVALID_ARG_VALUE", op, "", "unknown fs operation "+op)
	}
	a := &fsArgs{op: op, args: args}
	result, err := fn(a)
	if a.err != nil {
		return nil, a.err
	}
	return result, err
}

// fsOpArgs reads the (op, args) pair every fs builtin starts with.
func fsOpArgs(ctx *ESContext) (string, []any, bool) {
	if ctx.GetTop() < 2 || !ctx.IsString(0) || !ctx.IsArray(1) {
		return "", nil, false
	}
	// GetJSObject converts the stack top: bring the array there (the async
	// variant has its callback above it)
	ctx.Dup(1)
	converted := ctx.GetJSObject(-1)
	ctx.Pop()
	args, ok := converted.([]any)
	if !ok {
		return "", nil, false
	}
	return ctx.GetString(0), args, true
}

// _wbFsSync(op, args): run the operation now and return its result.
func (engine *ESEngine) esWbFsSync(ctx *ESContext) int {
	op, args, ok := fsOpArgs(ctx)
	if !ok {
		return duktape.DUK_RET_TYPE_ERROR
	}
	result, err := runFsOp(op, args)
	if err != nil {
		return throwFsError(ctx, err)
	}
	if result == nil {
		return 0
	}
	ctx.PushJSObject(result)
	return 1
}

// _wbFsAsync(op, args, callback): run the operation on a goroutine; the
// callback receives {error: descriptor} or {result: value} once, on the
// engine loop, in the file's cleanup scope. The stash entry is swept right
// after that invocation (or at the next single-threaded boundary if the
// stopped engine dropped the completion) - the same lifecycle as spawn.
func (engine *ESEngine) esWbFsAsync(ctx *ESContext) int {
	op, args, ok := fsOpArgs(ctx)
	if !ok || ctx.GetTop() != 3 || !ctx.IsFunction(2) {
		return duktape.DUK_RET_TYPE_ERROR
	}
	callbackKey := ctx.storeCallback(2)
	go func() {
		result, err := runFsOp(op, args)
		dropErr := engine.MaybeCallSync(func() {
			if !ctx.IsValid() {
				// the file was reloaded or removed: invalidate() swept the
				// stash entry, and the promise it settled died with the realm
				return
			}
			defer ctx.RemoveCallback(callbackKey)
			if currentFilename := ctx.GetCurrentFilename(); currentFilename != "" {
				engine.cleanup.PushCleanupScope(currentFilename)
				defer engine.cleanup.PopCleanupScope(currentFilename)
			}
			var reply map[string]any
			if err != nil {
				reply = map[string]any{"error": err.descriptor()}
			} else {
				reply = map[string]any{"result": result}
			}
			ctx.invokeCallback(callbackKey, objx.New(reply))
		})
		if dropErr != nil {
			engine.noteOrphanedCallback(ctx, callbackKey)
		}
	}()
	return 0
}

// ---------------------------------------------------------------------------
// operations

func fsTooLarge(path string, size int64) *fsError {
	msg := fmt.Sprintf("File size (%d) is greater than %d MiB", size, fsMaxReadFileSize>>20)
	if size < 0 {
		msg = fmt.Sprintf("File is larger than %d MiB", fsMaxReadFileSize>>20)
	}
	return newFsCodeError("ERR_FS_FILE_TOO_LARGE", "read", path, msg)
}

func fsReadFile(a *fsArgs) (any, *fsError) {
	path := a.str(0)
	f, err := os.Open(path)
	if err != nil {
		return nil, fsErrFromGo(err, "open", path)
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Mode().IsRegular() && st.Size() > fsMaxReadFileSize {
		return nil, fsTooLarge(path, st.Size())
	}
	data, err := io.ReadAll(io.LimitReader(f, fsMaxReadFileSize+1))
	if err != nil {
		return nil, fsErrFromGo(err, "read", path)
	}
	if len(data) > fsMaxReadFileSize {
		return nil, fsTooLarge(path, -1)
	}
	return string(data), nil
}

// fsOpenFlags are Node's file system flags (the "flag" option).
var fsOpenFlags = map[string]int{
	"r":   os.O_RDONLY,
	"r+":  os.O_RDWR,
	"w":   os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
	"wx":  os.O_WRONLY | os.O_CREATE | os.O_TRUNC | os.O_EXCL,
	"w+":  os.O_RDWR | os.O_CREATE | os.O_TRUNC,
	"wx+": os.O_RDWR | os.O_CREATE | os.O_TRUNC | os.O_EXCL,
	"a":   os.O_WRONLY | os.O_CREATE | os.O_APPEND,
	"ax":  os.O_WRONLY | os.O_CREATE | os.O_APPEND | os.O_EXCL,
	"a+":  os.O_RDWR | os.O_CREATE | os.O_APPEND,
	"ax+": os.O_RDWR | os.O_CREATE | os.O_APPEND | os.O_EXCL,
}

// writeFile(path, data, flag, mode)
func fsWriteFile(a *fsArgs) (any, *fsError) {
	path, data, flag, mode := a.str(0), a.str(1), a.str(2), a.num(3)
	if a.err != nil {
		return nil, a.err
	}
	flags, ok := fsOpenFlags[flag]
	if !ok {
		return nil, newFsCodeError("ERR_INVALID_ARG_VALUE", "open", path, "invalid file open flag "+flag)
	}
	f, err := os.OpenFile(path, flags, os.FileMode(uint32(mode)))
	if err != nil {
		return nil, fsErrFromGo(err, "open", path)
	}
	if _, err := f.WriteString(data); err != nil {
		f.Close()
		return nil, fsErrFromGo(err, "write", path)
	}
	if err := f.Close(); err != nil {
		return nil, fsErrFromGo(err, "close", path)
	}
	return nil, nil
}

func timespecMs(ts unix.Timespec) float64 {
	return float64(ts.Sec)*1e3 + float64(ts.Nsec)/1e6
}

func fsStat(a *fsArgs, follow bool) (any, *fsError) {
	path := a.str(0)
	var st unix.Stat_t
	var err error
	name := "stat"
	if follow {
		err = unix.Stat(path, &st)
	} else {
		name = "lstat"
		err = unix.Lstat(path, &st)
	}
	if err != nil {
		return nil, fsErrFromGo(err, name, path)
	}
	// Node's Stats fields; the JS class adds the is*() predicates and Date
	// views. birthtime is not available through stat(2) - ctime stands in,
	// as Node does on filesystems without it.
	return map[string]any{
		"dev":         float64(st.Dev),
		"ino":         float64(st.Ino),
		"mode":        float64(st.Mode),
		"nlink":       float64(st.Nlink),
		"uid":         float64(st.Uid),
		"gid":         float64(st.Gid),
		"rdev":        float64(st.Rdev),
		"size":        float64(st.Size),
		"blksize":     float64(st.Blksize),
		"blocks":      float64(st.Blocks),
		"atimeMs":     timespecMs(st.Atim),
		"mtimeMs":     timespecMs(st.Mtim),
		"ctimeMs":     timespecMs(st.Ctim),
		"birthtimeMs": timespecMs(st.Ctim),
	}, nil
}

// Node's dirent type constants (uv_dirent_type_t).
const (
	fsDirentUnknown = 0
	fsDirentFile    = 1
	fsDirentDir     = 2
	fsDirentLink    = 3
	fsDirentFifo    = 4
	fsDirentSocket  = 5
	fsDirentChar    = 6
	fsDirentBlock   = 7
)

func direntType(mode fs.FileMode) int {
	switch {
	case mode.IsRegular():
		return fsDirentFile
	case mode&fs.ModeDir != 0:
		return fsDirentDir
	case mode&fs.ModeSymlink != 0:
		return fsDirentLink
	case mode&fs.ModeNamedPipe != 0:
		return fsDirentFifo
	case mode&fs.ModeSocket != 0:
		return fsDirentSocket
	case mode&fs.ModeCharDevice != 0:
		return fsDirentChar
	case mode&fs.ModeDevice != 0:
		return fsDirentBlock
	}
	return fsDirentUnknown
}

// readdir(path, withFileTypes, recursive): names (relative to path when
// recursive) or {name, parentPath, type} dirents; entries of each
// directory come sorted by name, symlinked directories are not descended.
func fsReaddir(a *fsArgs) (any, *fsError) {
	root, withTypes, recursive := a.str(0), a.boolean(1), a.boolean(2)
	if a.err != nil {
		return nil, a.err
	}
	out := make([]any, 0)
	var walk func(dir, rel string) *fsError
	walk = func(dir, rel string) *fsError {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fsErrFromGo(err, "scandir", dir)
		}
		for _, e := range entries {
			relName := e.Name()
			if rel != "" {
				relName = rel + "/" + e.Name()
			}
			if withTypes {
				out = append(out, map[string]any{
					"name":       e.Name(),
					"parentPath": dir,
					"type":       direntType(e.Type()),
				})
			} else {
				out = append(out, relName)
			}
			if recursive && e.IsDir() {
				if err := walk(filepath.Join(dir, e.Name()), relName); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return nil, err
	}
	return out, nil
}

func fsExists(a *fsArgs) (any, *fsError) {
	path := a.str(0)
	if a.err != nil {
		return nil, a.err
	}
	return unix.Access(path, unix.F_OK) == nil, nil
}

// mkdir(path, recursive, mode): with recursive, returns the first
// directory created (undefined when nothing was), like Node.
func fsMkdir(a *fsArgs) (any, *fsError) {
	path, recursive, mode := a.str(0), a.boolean(1), a.num(2)
	if a.err != nil {
		return nil, a.err
	}
	if !recursive {
		if err := unix.Mkdir(path, uint32(mode)); err != nil {
			return nil, fsErrFromGo(err, "mkdir", path)
		}
		return nil, nil
	}
	clean := filepath.Clean(path)
	var first any
	prefix := ""
	if strings.HasPrefix(clean, "/") {
		prefix = "/"
	}
	for _, seg := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
		if seg == "" {
			continue
		}
		if prefix == "" || prefix == "/" {
			prefix += seg
		} else {
			prefix += "/" + seg
		}
		var st unix.Stat_t
		if err := unix.Stat(prefix, &st); err == nil {
			if st.Mode&unix.S_IFMT != unix.S_IFDIR {
				errno := syscall.EEXIST
				if prefix != clean {
					errno = syscall.ENOTDIR
				}
				return nil, newFsErrno(errno, "mkdir", path)
			}
			continue
		}
		if err := unix.Mkdir(prefix, uint32(mode)); err != nil && err != syscall.EEXIST {
			return nil, fsErrFromGo(err, "mkdir", path)
		}
		if first == nil {
			first = prefix
		}
	}
	return first, nil
}

func fsRmdir(a *fsArgs) (any, *fsError) {
	path := a.str(0)
	if a.err != nil {
		return nil, a.err
	}
	if err := unix.Rmdir(path); err != nil {
		return nil, fsErrFromGo(err, "rmdir", path)
	}
	return nil, nil
}

// rm(path, recursive, force)
func fsRm(a *fsArgs) (any, *fsError) {
	path, recursive, force := a.str(0), a.boolean(1), a.boolean(2)
	if a.err != nil {
		return nil, a.err
	}
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		if force && err == syscall.ENOENT {
			return nil, nil
		}
		return nil, fsErrFromGo(err, "lstat", path)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		if err := unix.Unlink(path); err != nil && !(force && err == syscall.ENOENT) {
			return nil, fsErrFromGo(err, "unlink", path)
		}
		return nil, nil
	}
	if !recursive {
		return nil, newFsCodeError("ERR_FS_EISDIR", "rm", path,
			"Path is a directory: rm returned EISDIR (is a directory) "+path)
	}
	if clean := filepath.Clean(path); clean == "/" || clean == "." {
		// never remove the root or the working directory wholesale
		return nil, newFsErrno(syscall.EPERM, "rm", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return nil, fsErrFromGo(err, "rm", path)
	}
	return nil, nil
}

func fsUnlink(a *fsArgs) (any, *fsError) {
	path := a.str(0)
	if a.err != nil {
		return nil, a.err
	}
	if err := unix.Unlink(path); err != nil {
		return nil, fsErrFromGo(err, "unlink", path)
	}
	return nil, nil
}

func fsRename(a *fsArgs) (any, *fsError) {
	from, to := a.str(0), a.str(1)
	if a.err != nil {
		return nil, a.err
	}
	if err := unix.Rename(from, to); err != nil {
		return nil, fsErrFromGoDest(err, "rename", from, to)
	}
	return nil, nil
}

// copyFile(src, dest, excl): a byte copy preserving the source's mode.
func fsCopyFile(a *fsArgs) (any, *fsError) {
	src, dest, excl := a.str(0), a.str(1), a.boolean(2)
	if a.err != nil {
		return nil, a.err
	}
	in, err := os.Open(src)
	if err != nil {
		return nil, fsErrFromGoDest(err, "copyfile", src, dest)
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return nil, fsErrFromGoDest(err, "copyfile", src, dest)
	}
	if st.IsDir() {
		return nil, newFsErrnoDest(syscall.EISDIR, "copyfile", src, dest)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if excl {
		flags |= os.O_EXCL
	}
	out, err := os.OpenFile(dest, flags, st.Mode().Perm())
	if err != nil {
		return nil, fsErrFromGoDest(err, "copyfile", src, dest)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return nil, fsErrFromGoDest(err, "copyfile", src, dest)
	}
	if err := out.Close(); err != nil {
		return nil, fsErrFromGoDest(err, "copyfile", src, dest)
	}
	// an existing destination keeps OpenFile's mode argument unapplied
	if err := os.Chmod(dest, st.Mode().Perm()); err != nil {
		return nil, fsErrFromGoDest(err, "copyfile", src, dest)
	}
	return nil, nil
}

func fsAccess(a *fsArgs) (any, *fsError) {
	path, mode := a.str(0), a.num(1)
	if a.err != nil {
		return nil, a.err
	}
	if err := unix.Access(path, uint32(mode)); err != nil {
		return nil, fsErrFromGo(err, "access", path)
	}
	return nil, nil
}

func fsRealpath(a *fsArgs) (any, *fsError) {
	path := a.str(0)
	if a.err != nil {
		return nil, a.err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fsErrFromGo(err, "realpath", path)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return nil, fsErrFromGo(err, "realpath", path)
	}
	return abs, nil
}

func fsReadlink(a *fsArgs) (any, *fsError) {
	path := a.str(0)
	if a.err != nil {
		return nil, a.err
	}
	target, err := os.Readlink(path)
	if err != nil {
		return nil, fsErrFromGo(err, "readlink", path)
	}
	return target, nil
}

// symlink(target, path): Node reports failures as target -> path.
func fsSymlink(a *fsArgs) (any, *fsError) {
	target, path := a.str(0), a.str(1)
	if a.err != nil {
		return nil, a.err
	}
	if err := unix.Symlink(target, path); err != nil {
		return nil, fsErrFromGoDest(err, "symlink", target, path)
	}
	return nil, nil
}

func fsChmod(a *fsArgs) (any, *fsError) {
	path, mode := a.str(0), a.num(1)
	if a.err != nil {
		return nil, a.err
	}
	if err := unix.Chmod(path, uint32(mode)); err != nil {
		return nil, fsErrFromGo(err, "chmod", path)
	}
	return nil, nil
}

const fsTempChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// mkdtemp(prefix): prefix + six random characters, mode 0700.
func fsMkdtemp(a *fsArgs) (any, *fsError) {
	prefix := a.str(0)
	if a.err != nil {
		return nil, a.err
	}
	for attempt := 0; attempt < 100; attempt++ {
		var raw [6]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, fsErrFromGo(err, "mkdtemp", prefix+"XXXXXX")
		}
		suffix := make([]byte, len(raw))
		for i, b := range raw {
			suffix[i] = fsTempChars[int(b)%len(fsTempChars)]
		}
		path := prefix + string(suffix)
		err := unix.Mkdir(path, 0o700)
		if err == nil {
			return path, nil
		}
		if err != syscall.EEXIST {
			return nil, fsErrFromGo(err, "mkdtemp", prefix+"XXXXXX")
		}
	}
	return nil, newFsErrno(syscall.EEXIST, "mkdtemp", prefix+"XXXXXX")
}

// truncate(path, length): Node opens the file first, so a missing one
// reports the open.
func fsTruncate(a *fsArgs) (any, *fsError) {
	path, length := a.str(0), a.num(1)
	if a.err != nil {
		return nil, a.err
	}
	if err := unix.Truncate(path, int64(length)); err != nil {
		name := "ftruncate"
		if err == syscall.ENOENT {
			name = "open"
		}
		return nil, fsErrFromGo(err, name, path)
	}
	return nil, nil
}

// utimes(path, atimeSec, mtimeSec)
func fsUtimes(a *fsArgs) (any, *fsError) {
	path, atime, mtime := a.str(0), a.num(1), a.num(2)
	if a.err != nil {
		return nil, a.err
	}
	ts := []unix.Timespec{unix.NsecToTimespec(int64(atime * 1e9)), unix.NsecToTimespec(int64(mtime * 1e9))}
	if err := unix.UtimesNano(path, ts); err != nil {
		return nil, fsErrFromGo(err, "utime", path)
	}
	return nil, nil
}

// fsLogWarning is the module's log hook for events that have no promise
// or callback to report through (an overflowed inotify queue).
func (engine *ESEngine) fsLogWarning(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	wbgong.Warn.Println(msg)
	engine.Log(ENGINE_LOG_WARNING, msg)
}
