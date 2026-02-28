package wbrules

import (
	"errors"
	"fmt"
	"os"

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

	info, err := os.Stat(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFileSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	if info.Size() > maxReadFileSize {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFileSync() failed: file %s is too large (%d bytes, max %d)", path, info.Size(), maxReadFileSize))
		return duktape.DUK_RET_ERROR
	}

	data, err := os.ReadFile(path)
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
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.existsSync() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
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
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.unlinkSync() failed: %s is a directory, use fs.rmdir() or remove manually", path))
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

		info, err := os.Stat(path)
		if err != nil {
			errObj = fsErrObj(err)
		} else if info.Size() > maxReadFileSize {
			errObj = fsErrObj(fmt.Errorf("file %s is too large (%d bytes, max %d)", path, info.Size(), maxReadFileSize))
		} else {
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				errObj = fsErrObj(readErr)
			} else {
				data = string(raw)
			}
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
		var writeErr error
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, writeErr = f.WriteString(data)
			f.Close()
			if writeErr != nil {
				err = writeErr
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
