package wbrules

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/fsnotify/fsnotify"
	duktape "github.com/wirenboard/go-duktape"
	"github.com/wirenboard/wbgong"
)

const maxReadFileSize = 10 * 1024 * 1024 // 10 MB

// fsAsyncCallbackPreamble checks ctx validity and pushes cleanup scope inside CallSync.
// Returns ("", false) if context is no longer valid (caller should return).
// On success returns (filename, true); the caller must defer fsAsyncCallbackPostamble(filename).
func (engine *ESEngine) fsAsyncCallbackPreamble(ctx *ESContext) (string, bool) {
	if !ctx.IsValid() {
		wbgong.Info.Println("ignore fs callback without Duktape context (maybe script is reloaded or removed)")
		return "", false
	}

	filename := ctx.GetCurrentFilename()
	if filename != "" {
		engine.cleanup.PushCleanupScope(filename)
	}
	return filename, true
}

func (engine *ESEngine) fsAsyncCallbackPostamble(filename string) {
	if filename != "" {
		engine.cleanup.PopCleanupScope(filename)
	}
}

func fsErrObj(err error) map[string]any {
	return map[string]any{"message": err.Error()}
}

// fsAsyncCallback is a helper that wraps the common CallSync + preamble/postamble + error-first
// callback pattern used by all async fs functions (except fs.exists and fs.watch).
// If err != nil, callback receives (errObj, nil, nil, ...) with nils matching the count of results.
// If err == nil, callback receives (nil, results...).
func (engine *ESEngine) fsAsyncCallback(ctx *ESContext, callbackFn func(...any), err error, results ...any) {
	engine.CallSync(func() {
		filename, ok := engine.fsAsyncCallbackPreamble(ctx)
		if !ok {
			return
		}
		defer engine.fsAsyncCallbackPostamble(filename)

		if err != nil {
			args := make([]any, 1+len(results))
			args[0] = fsErrObj(err)
			callbackFn(args...)
		} else {
			args := make([]any, 1+len(results))
			args[0] = nil
			copy(args[1:], results)
			callbackFn(args...)
		}
	})
}

func buildStatObj(info os.FileInfo) map[string]any {
	return map[string]any{
		"size":        info.Size(),
		"isFile":      info.Mode().IsRegular(),
		"isDirectory": info.IsDir(),
		"mtime":       info.ModTime().Unix(),
		"mode":        fmt.Sprintf("%o", info.Mode().Perm()),
	}
}

func buildDirEntry(name string, info os.FileInfo) map[string]any {
	return map[string]any{
		"name":        name,
		"isFile":      info.Mode().IsRegular(),
		"isDirectory": info.IsDir(),
	}
}

// ──────────────────────────────────────────────
// Sync functions
// ──────────────────────────────────────────────

// fs.readFileSync(path) -> string
func (engine *ESEngine) esFileReadFileSync(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.readFileSync(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)

	f, err := os.Open(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxReadFileSize+1))
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	if int64(len(data)) > maxReadFileSize {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFileSync() failed: file %s is too large (max %d bytes)", path, maxReadFileSize))
		return duktape.DUK_RET_ERROR
	}

	ctx.PushString(string(data))
	return 1
}

// atomicWriteFile writes data to path atomically via temp file + rename.
// This ensures the file is never left in a partially-written state on power loss.
// If the target file already exists, its permissions are preserved.
func atomicWriteFile(path string, data []byte, defaultPerm os.FileMode) error {
	// Preserve existing file permissions if the file already exists
	perm := defaultPerm
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	dir := filepath.Dir(path)

	// Verify the target directory exists before creating temp file,
	// so error messages reference the original path, not the temp file
	if _, err := os.Stat(dir); err != nil {
		pathErr := &os.PathError{Op: "open", Path: path}
		var pe *os.PathError
		if errors.As(err, &pe) {
			pathErr.Err = pe.Err
		} else {
			pathErr.Err = err
		}
		return pathErr
	}

	tmp, err := os.CreateTemp(dir, ".wb-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	_, writeErr := tmp.Write(data)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()

	if writeErr != nil {
		os.Remove(tmpName)
		return writeErr
	}
	if syncErr != nil {
		os.Remove(tmpName)
		return syncErr
	}
	if closeErr != nil {
		os.Remove(tmpName)
		return closeErr
	}

	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// fs.writeFileSync(path, data)
func (engine *ESEngine) esFileWriteFileSync(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.writeFileSync(): expected (path, data)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	data := ctx.GetString(1)
	if err := atomicWriteFile(path, []byte(data), 0o644); err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.writeFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.appendFileSync(path, data)
func (engine *ESEngine) esFileAppendFileSync(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.appendFileSync(): expected (path, data)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	data := ctx.GetString(1)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.appendFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	_, writeErr := f.WriteString(data)
	closeErr := f.Close()
	if writeErr != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.appendFileSync() failed: %s", writeErr))
		return duktape.DUK_RET_ERROR
	}
	if closeErr != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.appendFileSync() failed: %s", closeErr))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.statSync(path) -> {size, isFile, isDirectory, mtime, mode}
func (engine *ESEngine) esFileStatSync(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.statSync(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	info, err := os.Stat(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.statSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	ctx.PushJSObject(buildStatObj(info))
	return 1
}

// fs.readdirSync(path) -> [{name, isFile, isDirectory}, ...]
func (engine *ESEngine) esFileReaddirSync(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.readdirSync(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	entries, err := os.ReadDir(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readdirSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	result := make([]any, len(entries))
	for i, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readdirSync() failed: %s", err))
			return duktape.DUK_RET_ERROR
		}
		result[i] = buildDirEntry(entry.Name(), info)
	}

	ctx.PushJSObject(result)
	return 1
}

// fs.existsSync(path) -> bool
func (engine *ESEngine) esFileExistsSync(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.existsSync(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	_, err := os.Stat(path)
	ctx.PushBoolean(err == nil)
	return 1
}

// fs.mkdirSync(path [, {recursive: true}])
func (engine *ESEngine) esFileMkdirSync(ctx *ESContext) int {
	numArgs := ctx.GetTop()
	if numArgs < 1 || numArgs > 2 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.mkdirSync(): expected (path [, {recursive}])")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	recursive := false

	if numArgs == 2 && ctx.IsObject(1) {
		ctx.GetPropString(1, "recursive")
		if ctx.IsBoolean(-1) {
			recursive = ctx.GetBoolean(-1)
		}
		ctx.Pop()
	}

	var err error
	if recursive {
		err = os.MkdirAll(path, 0o755)
	} else {
		err = os.Mkdir(path, 0o755)
	}

	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.mkdirSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.unlinkSync(path) — removes a file (not a directory)
func (engine *ESEngine) esFileUnlinkSync(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.unlinkSync(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)

	info, err := os.Lstat(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.unlinkSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	if info.IsDir() {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.unlinkSync() failed: %s is a directory, use fs.rmdirSync() or remove manually", path))
		return duktape.DUK_RET_ERROR
	}

	if err := os.Remove(path); err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.unlinkSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.renameSync(oldPath, newPath)
func (engine *ESEngine) esFileRenameSync(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.renameSync(): expected (oldPath, newPath)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	oldPath := ctx.GetString(0)
	newPath := ctx.GetString(1)
	if err := os.Rename(oldPath, newPath); err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.renameSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.rmdirSync(path [, {recursive: true}])
func (engine *ESEngine) esFileRmdirSync(ctx *ESContext) int {
	numArgs := ctx.GetTop()
	if numArgs < 1 || numArgs > 2 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.rmdirSync(): expected (path [, {recursive}])")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	recursive := false

	if numArgs == 2 && ctx.IsObject(1) {
		ctx.GetPropString(1, "recursive")
		if ctx.IsBoolean(-1) {
			recursive = ctx.GetBoolean(-1)
		}
		ctx.Pop()
	}

	info, err := os.Lstat(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.rmdirSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	if !info.IsDir() {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.rmdirSync() failed: %s is not a directory", path))
		return duktape.DUK_RET_ERROR
	}

	if recursive {
		cleanPath := filepath.Clean(path)
		if cleanPath == "/" || cleanPath == "." {
			engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.rmdirSync() failed: refusing to recursively remove %q", path))
			return duktape.DUK_RET_ERROR
		}
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}

	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.rmdirSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.copyFileSync(src, dest)
func (engine *ESEngine) esFileCopyFileSync(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.copyFileSync(): expected (src, dest)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	src := ctx.GetString(0)
	dest := ctx.GetString(1)

	// Guard against self-copy: use os.SameFile to handle symlinks and hard links
	if srcInfo, err := os.Stat(src); err == nil {
		if destInfo, err := os.Stat(dest); err == nil && os.SameFile(srcInfo, destInfo) {
			return 0
		}
	}

	srcFile, err := os.Open(src)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.copyFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	defer srcFile.Close()

	info, err := srcFile.Stat()
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.copyFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	destFile, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.copyFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	_, copyErr := io.Copy(destFile, srcFile)
	closeErr := destFile.Close()
	if copyErr != nil {
		os.Remove(dest)
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.copyFileSync() failed: %s", copyErr))
		return duktape.DUK_RET_ERROR
	}
	if closeErr != nil {
		os.Remove(dest)
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.copyFileSync() failed: %s", closeErr))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.accessSync(path [, mode])
func (engine *ESEngine) esFileAccessSync(ctx *ESContext) int {
	numArgs := ctx.GetTop()
	if numArgs < 1 || numArgs > 2 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.accessSync(): expected (path [, mode])")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	mode := uint32(syscall.F_OK)

	if numArgs == 2 && ctx.IsNumber(1) {
		mode = uint32(ctx.GetNumber(1))
	}

	if mode > 7 {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.accessSync() failed: invalid mode %d", mode))
		return duktape.DUK_RET_ERROR
	}

	if err := syscall.Access(path, mode); err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.accessSync() failed: access %s: %s", path, err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.realpathSync(path) -> string
func (engine *ESEngine) esFileRealpathSync(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.realpathSync(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.realpathSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.realpathSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	ctx.PushString(resolved)
	return 1
}

// fs.readlinkSync(path) -> string
func (engine *ESEngine) esFileReadlinkSync(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.readlinkSync(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	target, err := os.Readlink(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readlinkSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	ctx.PushString(target)
	return 1
}

// ──────────────────────────────────────────────
// Async functions (error-first callback pattern)
// ──────────────────────────────────────────────

// fs.readFile(path, callback) — callback(err, data)
func (engine *ESEngine) esFileReadFile(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.readFile(): expected (path, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	callbackFn := ctx.WrapCallbackArgs(1)

	go func() {
		var data string
		var readErr error

		f, err := os.Open(path)
		if err != nil {
			readErr = err
		} else {
			raw, err := io.ReadAll(io.LimitReader(f, maxReadFileSize+1))
			f.Close()
			if err != nil {
				readErr = err
			} else if int64(len(raw)) > maxReadFileSize {
				readErr = fmt.Errorf("file %s is too large (max %d bytes)", path, maxReadFileSize)
			} else {
				data = string(raw)
			}
		}

		engine.fsAsyncCallback(ctx, callbackFn, readErr, data)
	}()

	return 0
}

// fs.writeFile(path, data, callback) — callback(err)
func (engine *ESEngine) esFileWriteFile(ctx *ESContext) int {
	if ctx.GetTop() != 3 || !ctx.IsString(0) || !ctx.IsString(1) || !ctx.IsFunction(2) {
		engine.Log(ENGINE_LOG_ERROR, "fs.writeFile(): expected (path, data, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	data := ctx.GetString(1)
	callbackFn := ctx.WrapCallbackArgs(2)

	go func() {
		err := atomicWriteFile(path, []byte(data), 0o644)

		engine.fsAsyncCallback(ctx, callbackFn, err)
	}()

	return 0
}

// fs.appendFile(path, data, callback) — callback(err)
func (engine *ESEngine) esFileAppendFile(ctx *ESContext) int {
	if ctx.GetTop() != 3 || !ctx.IsString(0) || !ctx.IsString(1) || !ctx.IsFunction(2) {
		engine.Log(ENGINE_LOG_ERROR, "fs.appendFile(): expected (path, data, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	data := ctx.GetString(1)
	callbackFn := ctx.WrapCallbackArgs(2)

	go func() {
		var err error
		f, openErr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if openErr != nil {
			err = openErr
		} else {
			_, writeErr := f.WriteString(data)
			closeErr := f.Close()
			if writeErr != nil {
				err = writeErr
			} else if closeErr != nil {
				err = closeErr
			}
		}

		engine.fsAsyncCallback(ctx, callbackFn, err)
	}()

	return 0
}

// fs.stat(path, callback) — callback(err, stats)
func (engine *ESEngine) esFileStat(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.stat(): expected (path, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	callbackFn := ctx.WrapCallbackArgs(1)

	go func() {
		info, err := os.Stat(path)
		var result map[string]any
		if err == nil {
			result = buildStatObj(info)
		}
		engine.fsAsyncCallback(ctx, callbackFn, err, result)
	}()

	return 0
}

// fs.readdir(path, callback) — callback(err, entries)
func (engine *ESEngine) esFileReaddir(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.readdir(): expected (path, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	callbackFn := ctx.WrapCallbackArgs(1)

	go func() {
		entries, err := os.ReadDir(path)
		var result []any
		if err == nil {
			result = make([]any, len(entries))
			for i, entry := range entries {
				info, infoErr := entry.Info()
				if infoErr != nil {
					err = infoErr
					break
				}
				result[i] = buildDirEntry(entry.Name(), info)
			}
		}

		engine.fsAsyncCallback(ctx, callbackFn, err, result)
	}()

	return 0
}

// fs.exists(path, callback) — callback(exists)  (no error argument, Node.js convention).
// Note: unlike existsSync, permission errors are silently treated as "not exists"
// because the callback signature has no error parameter (matches Node.js behavior).
func (engine *ESEngine) esFileExists(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.exists(): expected (path, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	callbackFn := ctx.WrapCallbackArgs(1)

	go func() {
		_, err := os.Stat(path)
		exists := err == nil

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			callbackFn(exists)
		})
	}()

	return 0
}

// fs.mkdir(path, [opts,] callback) — callback(err)
func (engine *ESEngine) esFileMkdir(ctx *ESContext) int {
	numArgs := ctx.GetTop()
	if numArgs < 2 || numArgs > 3 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.mkdir(): expected (path, [opts,] callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	recursive := false
	callbackIdx := numArgs - 1

	if !ctx.IsFunction(callbackIdx) {
		engine.Log(ENGINE_LOG_ERROR, "fs.mkdir(): last argument must be a callback function")
		return duktape.DUK_RET_TYPE_ERROR
	}

	if numArgs == 3 && ctx.IsObject(1) {
		ctx.GetPropString(1, "recursive")
		if ctx.IsBoolean(-1) {
			recursive = ctx.GetBoolean(-1)
		}
		ctx.Pop()
	}

	callbackFn := ctx.WrapCallbackArgs(callbackIdx)

	go func() {
		var err error
		if recursive {
			err = os.MkdirAll(path, 0o755)
		} else {
			err = os.Mkdir(path, 0o755)
		}

		engine.fsAsyncCallback(ctx, callbackFn, err)
	}()

	return 0
}

// fs.unlink(path, callback) — callback(err)
func (engine *ESEngine) esFileUnlink(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.unlink(): expected (path, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	callbackFn := ctx.WrapCallbackArgs(1)

	go func() {
		var err error
		info, statErr := os.Lstat(path)
		if statErr != nil {
			err = statErr
		} else if info.IsDir() {
			err = fmt.Errorf("%s is a directory, use fs.rmdir() or remove manually", path)
		} else {
			err = os.Remove(path)
		}

		engine.fsAsyncCallback(ctx, callbackFn, err)
	}()

	return 0
}

// fs.rename(oldPath, newPath, callback) — callback(err)
func (engine *ESEngine) esFileRename(ctx *ESContext) int {
	if ctx.GetTop() != 3 || !ctx.IsString(0) || !ctx.IsString(1) || !ctx.IsFunction(2) {
		engine.Log(ENGINE_LOG_ERROR, "fs.rename(): expected (oldPath, newPath, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	oldPath := ctx.GetString(0)
	newPath := ctx.GetString(1)
	callbackFn := ctx.WrapCallbackArgs(2)

	go func() {
		err := os.Rename(oldPath, newPath)

		engine.fsAsyncCallback(ctx, callbackFn, err)
	}()

	return 0
}

// fs.rmdir(path, [opts,] callback) — callback(err)
func (engine *ESEngine) esFileRmdir(ctx *ESContext) int {
	numArgs := ctx.GetTop()
	if numArgs < 2 || numArgs > 3 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.rmdir(): expected (path, [opts,] callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	recursive := false
	callbackIdx := numArgs - 1

	if !ctx.IsFunction(callbackIdx) {
		engine.Log(ENGINE_LOG_ERROR, "fs.rmdir(): last argument must be a callback function")
		return duktape.DUK_RET_TYPE_ERROR
	}

	if numArgs == 3 && ctx.IsObject(1) {
		ctx.GetPropString(1, "recursive")
		if ctx.IsBoolean(-1) {
			recursive = ctx.GetBoolean(-1)
		}
		ctx.Pop()
	}

	callbackFn := ctx.WrapCallbackArgs(callbackIdx)

	go func() {
		var err error
		info, statErr := os.Lstat(path)
		if statErr != nil {
			err = statErr
		} else if !info.IsDir() {
			err = fmt.Errorf("%s is not a directory", path)
		} else if recursive {
			cleanPath := filepath.Clean(path)
			if cleanPath == "/" || cleanPath == "." {
				err = fmt.Errorf("refusing to recursively remove %q", path)
			} else {
				err = os.RemoveAll(path)
			}
		} else {
			err = os.Remove(path)
		}

		engine.fsAsyncCallback(ctx, callbackFn, err)
	}()

	return 0
}

// fs.copyFile(src, dest, callback) — callback(err)
func (engine *ESEngine) esFileCopyFile(ctx *ESContext) int {
	if ctx.GetTop() != 3 || !ctx.IsString(0) || !ctx.IsString(1) || !ctx.IsFunction(2) {
		engine.Log(ENGINE_LOG_ERROR, "fs.copyFile(): expected (src, dest, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	src := ctx.GetString(0)
	dest := ctx.GetString(1)
	callbackFn := ctx.WrapCallbackArgs(2)

	// Guard against self-copy: use os.SameFile to handle symlinks and hard links
	if srcInfo, statErr := os.Stat(src); statErr == nil {
		if destInfo, statErr := os.Stat(dest); statErr == nil && os.SameFile(srcInfo, destInfo) {
			go func() {
				engine.fsAsyncCallback(ctx, callbackFn, nil)
			}()
			return 0
		}
	}

	go func() {
		var err error
		srcFile, openErr := os.Open(src)
		if openErr != nil {
			err = openErr
		} else {
			var info os.FileInfo
			info, err = srcFile.Stat()
			if err == nil {
				var destFile *os.File
				destFile, err = os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
				if err == nil {
					_, err = io.Copy(destFile, srcFile)
					closeErr := destFile.Close()
					if err == nil {
						err = closeErr
					}
					if err != nil {
						os.Remove(dest)
					}
				}
			}
			srcFile.Close()
		}

		engine.fsAsyncCallback(ctx, callbackFn, err)
	}()

	return 0
}

// fs.access(path, [mode,] callback) — callback(err)
func (engine *ESEngine) esFileAccess(ctx *ESContext) int {
	numArgs := ctx.GetTop()
	if numArgs < 2 || numArgs > 3 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.access(): expected (path, [mode,] callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	mode := uint32(syscall.F_OK)
	callbackIdx := numArgs - 1

	if !ctx.IsFunction(callbackIdx) {
		engine.Log(ENGINE_LOG_ERROR, "fs.access(): last argument must be a callback function")
		return duktape.DUK_RET_TYPE_ERROR
	}

	if numArgs == 3 && ctx.IsNumber(1) {
		mode = uint32(ctx.GetNumber(1))
	}

	if mode > 7 {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.access() failed: invalid mode %d", mode))
		return duktape.DUK_RET_ERROR
	}

	callbackFn := ctx.WrapCallbackArgs(callbackIdx)

	go func() {
		var accessErr error
		if err := syscall.Access(path, mode); err != nil {
			accessErr = fmt.Errorf("access %s: %w", path, err)
		}

		engine.fsAsyncCallback(ctx, callbackFn, accessErr)
	}()

	return 0
}

// fs.realpath(path, callback) — callback(err, resolvedPath)
func (engine *ESEngine) esFileRealpath(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.realpath(): expected (path, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	callbackFn := ctx.WrapCallbackArgs(1)

	go func() {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			resolved, err = filepath.Abs(resolved)
		}
		engine.fsAsyncCallback(ctx, callbackFn, err, resolved)
	}()

	return 0
}

// fs.readlink(path, callback) — callback(err, target)
func (engine *ESEngine) esFileReadlink(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.readlink(): expected (path, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	callbackFn := ctx.WrapCallbackArgs(1)

	go func() {
		target, err := os.Readlink(path)
		engine.fsAsyncCallback(ctx, callbackFn, err, target)
	}()

	return 0
}

// fs.watch(path, callback) — returns {close: function()}
// callback(eventType, filename) where eventType is "change" or "rename"
func (engine *ESEngine) esFileWatch(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.watch(): expected (path, callback)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	callbackFn := ctx.WrapCallbackArgs(1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.watch() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	if err := watcher.Add(path); err != nil {
		watcher.Close()
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.watch() failed: %s: %s", path, err))
		return duktape.DUK_RET_ERROR
	}

	var closeOnce sync.Once
	closeFn := func() {
		closeOnce.Do(func() {
			watcher.Close()
		})
	}

	// Register cleanup so watcher is closed on script reload
	scriptFilename := ctx.GetCurrentFilename()
	if scriptFilename != "" {
		engine.cleanup.PushCleanupScope(scriptFilename)
		engine.cleanup.AddCleanup(closeFn)
		engine.cleanup.PopCleanupScope(scriptFilename)
	} else {
		wbgong.Warn.Printf("fs.watch(%s): cannot register cleanup (no script filename), watcher will leak on reload", path)
	}

	// Start goroutine to relay events
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				var eventType string
				switch {
				case event.Has(fsnotify.Write) || event.Has(fsnotify.Chmod):
					eventType = "change"
				default:
					eventType = "rename"
				}
				eventFilename := filepath.Base(event.Name)

				engine.CallSync(func() {
					filename, ok := engine.fsAsyncCallbackPreamble(ctx)
					if !ok {
						closeFn()
						return
					}
					defer engine.fsAsyncCallbackPostamble(filename)

					callbackFn(eventType, eventFilename)
				})
			case watchErr, ok := <-watcher.Errors:
				if !ok {
					return
				}
				wbgong.Error.Printf("fs.watch() error on %s: %s", path, watchErr)
			}
		}
	}()

	// Return watcher object with close() method
	ctx.PushObject()
	ctx.DefineFunctions(map[string]func(*ESContext) int{
		"close": func(_ *ESContext) int {
			closeFn()
			return 0
		},
	})

	return 1
}
