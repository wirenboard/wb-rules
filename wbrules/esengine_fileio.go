package wbrules

import (
	"errors"
	"fmt"
	"os"

	duktape "github.com/wirenboard/go-duktape"
)

const maxReadFileSize = 10 * 1024 * 1024 // 10 MB

// fs.readFile(path) -> string
func (engine *ESEngine) esFileReadFile(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.readFile(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)

	info, err := os.Stat(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFile() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	if info.Size() > maxReadFileSize {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFile() failed: file %s is too large (%d bytes, max %d)", path, info.Size(), maxReadFileSize))
		return duktape.DUK_RET_ERROR
	}

	data, err := os.ReadFile(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readFile() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	ctx.PushString(string(data))
	return 1
}

// fs.writeFile(path, data)
func (engine *ESEngine) esFileWriteFile(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.writeFile(): expected (path, data)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	data := ctx.GetString(1)
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.writeFile() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.appendFile(path, data)
func (engine *ESEngine) esFileAppendFile(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.appendFile(): expected (path, data)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	data := ctx.GetString(1)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.appendFile() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	defer f.Close()

	if _, err := f.WriteString(data); err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.appendFile() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.stat(path) -> {size, isFile, isDirectory, mtime, mode}
func (engine *ESEngine) esFileStat(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.stat(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	info, err := os.Stat(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.stat() failed: %s", err))
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

// fs.readDir(path) -> [{name, isFile, isDirectory}, ...]
func (engine *ESEngine) esFileReadDir(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.readDir(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	entries, err := os.ReadDir(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readDir() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	result := make([]interface{}, len(entries))
	for i, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.readDir() failed: %s", err))
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

// fs.exists(path) -> bool
func (engine *ESEngine) esFileExists(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.exists(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.exists() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	ctx.PushBoolean(err == nil)
	return 1
}

// fs.mkdir(path [, {recursive: true}])
func (engine *ESEngine) esFileMkdir(ctx *ESContext) int {
	numArgs := ctx.GetTop()
	if numArgs < 1 || numArgs > 2 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.mkdir(): expected (path [, {recursive}])")
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
		err = os.MkdirAll(path, 0755)
	} else {
		err = os.Mkdir(path, 0755)
	}

	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.mkdir() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.unlink(path) — removes a file (not a directory)
func (engine *ESEngine) esFileUnlink(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "fs.unlink(): expected (path)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	path := ctx.GetString(0)

	info, err := os.Lstat(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.unlink() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}
	if info.IsDir() {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.unlink() failed: %s is a directory, use fs.rmdir() or remove manually", path))
		return duktape.DUK_RET_ERROR
	}

	if err := os.Remove(path); err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.unlink() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

// fs.rename(oldPath, newPath)
func (engine *ESEngine) esFileRename(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, "fs.rename(): expected (oldPath, newPath)")
		return duktape.DUK_RET_TYPE_ERROR
	}

	oldPath := ctx.GetString(0)
	newPath := ctx.GetString(1)
	if err := os.Rename(oldPath, newPath); err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("fs.rename() failed: %s", err))
		return duktape.DUK_RET_ERROR
	}

	return 0
}
