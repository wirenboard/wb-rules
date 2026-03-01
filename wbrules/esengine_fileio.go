package wbrules

import (
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

func fsErrObj(err error) map[string]interface{} {
	return map[string]interface{}{"message": err.Error()}
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

	info, err := f.Stat()
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	if info.Size() > maxReadFileSize {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFileSync() failed: file %s is too large (%d bytes, max %d)", path, info.Size(), maxReadFileSize))
		return duktape.DUK_RET_ERROR
	}

	data, err := io.ReadAll(f)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	ctx.PushString(string(data))
	return 1
}

// fs.writeFileSync(path, data)
func (engine *ESEngine) esFileWriteFileSync(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.writeFileSync(): expected (path, data)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	data := ctx.GetString(1)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
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
	defer f.Close()

	if _, err := f.WriteString(data); err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.appendFileSync() failed: %s", err))
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

	ctx.PushJSObject(map[string]interface{}{
		"size":        info.Size(),
		"isFile":      info.Mode().IsRegular(),
		"isDirectory": info.IsDir(),
		"mtime":       info.ModTime().Unix(),
		"mode":        fmt.Sprintf("%o", info.Mode().Perm()),
	})
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

	result := make([]interface{}, len(entries))
	for i, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readdirSync() failed: %s", err))
			return duktape.DUK_RET_ERROR
		}
		result[i] = map[string]interface{}{
			"name":        entry.Name(),
			"isFile":      info.Mode().IsRegular(),
			"isDirectory": info.IsDir(),
		}
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
	if info.Size() > maxReadFileSize {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.copyFileSync() failed: file %s is too large (%d bytes, max %d)", src, info.Size(), maxReadFileSize))
		return duktape.DUK_RET_ERROR
	}

	destFile, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.copyFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.copyFileSync() failed: %s", err))
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
		var errObj map[string]interface{}
		var data string

		f, err := os.Open(path)
		if err != nil {
			errObj = fsErrObj(err)
		} else {
			info, statErr := f.Stat()
			if statErr != nil {
				errObj = fsErrObj(statErr)
			} else if info.Size() > maxReadFileSize {
				errObj = fsErrObj(fmt.Errorf("file %s is too large (%d bytes, max %d)", path, info.Size(), maxReadFileSize))
			} else {
				raw, readErr := io.ReadAll(f)
				if readErr != nil {
					errObj = fsErrObj(readErr)
				} else {
					data = string(raw)
				}
			}
			f.Close()
		}

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if errObj != nil {
				callbackFn(errObj, nil)
			} else {
				callbackFn(nil, data)
			}
		})
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
		err := os.WriteFile(path, []byte(data), 0o644)

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err))
			} else {
				callbackFn(nil)
			}
		})
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

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err))
			} else {
				callbackFn(nil)
			}
		})
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

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err), nil)
			} else {
				callbackFn(nil, map[string]interface{}{
					"size":        info.Size(),
					"isFile":      info.Mode().IsRegular(),
					"isDirectory": info.IsDir(),
					"mtime":       info.ModTime().Unix(),
					"mode":        fmt.Sprintf("%o", info.Mode().Perm()),
				})
			}
		})
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
		var result []interface{}
		if err == nil {
			result = make([]interface{}, len(entries))
			for i, entry := range entries {
				info, infoErr := entry.Info()
				if infoErr != nil {
					err = infoErr
					break
				}
				result[i] = map[string]interface{}{
					"name":        entry.Name(),
					"isFile":      info.Mode().IsRegular(),
					"isDirectory": info.IsDir(),
				}
			}
		}

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err), nil)
			} else {
				callbackFn(nil, result)
			}
		})
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

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err))
			} else {
				callbackFn(nil)
			}
		})
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

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err))
			} else {
				callbackFn(nil)
			}
		})
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

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err))
			} else {
				callbackFn(nil)
			}
		})
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

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err))
			} else {
				callbackFn(nil)
			}
		})
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

	go func() {
		var err error
		srcFile, openErr := os.Open(src)
		if openErr != nil {
			err = openErr
		} else {
			var info os.FileInfo
			info, err = srcFile.Stat()
			if err == nil {
				if info.Size() > maxReadFileSize {
					err = fmt.Errorf("file %s is too large (%d bytes, max %d)", src, info.Size(), maxReadFileSize)
				} else {
					var destFile *os.File
					destFile, err = os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
					if err == nil {
						_, err = io.Copy(destFile, srcFile)
						closeErr := destFile.Close()
						if err == nil {
							err = closeErr
						}
					}
				}
			}
			srcFile.Close()
		}

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err))
			} else {
				callbackFn(nil)
			}
		})
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

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if accessErr != nil {
				callbackFn(fsErrObj(accessErr))
			} else {
				callbackFn(nil)
			}
		})
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

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err), nil)
			} else {
				callbackFn(nil, resolved)
			}
		})
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

		engine.CallSync(func() {
			filename, ok := engine.fsAsyncCallbackPreamble(ctx)
			if !ok {
				return
			}
			defer engine.fsAsyncCallbackPostamble(filename)

			if err != nil {
				callbackFn(fsErrObj(err), nil)
			} else {
				callbackFn(nil, target)
			}
		})
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
