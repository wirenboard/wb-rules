package wbrules

import (
	"errors"
	"github.com/contactless/wbgo/testutils"
	"github.com/stretchr/objx"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type EditorSuite struct {
	testutils.Suite
	*testutils.DataFileFixture
	*testutils.RpcFixture
	liveWritePath   string
	liveWriteError  error
	scriptErrorPath string
	scriptError     *ScriptError
}

func (s *EditorSuite) T() *testing.T {
	return s.Suite.T()
}

func (s *EditorSuite) SetupTest() {
	s.Suite.SetupTest()
	s.liveWritePath = ""
	s.liveWriteError = nil
	s.scriptErrorPath = ""
	s.scriptError = nil
	s.DataFileFixture = testutils.NewDataFileFixture(s.T())
	s.addSampleFiles()
	s.RpcFixture = testutils.NewRpcFixture(
		s.T(), "wbrules", "Editor", "wbrules",
		NewEditor(s),
		"List", "Load", "Remove", "Save")
}

func (s *EditorSuite) TearDownTest() {
	s.TearDownRPC()
	s.TearDownDataFiles()
	s.Suite.TearDownTest()
}

func (s *EditorSuite) ScriptDir() string {
	return s.DataFileTempDir()
}

func (s *EditorSuite) LiveWriteScript(virtualPath, content string) error {
	if s.liveWritePath == "" {
		s.Require().Fail("unexpected LiveWriteDataFile()")
	}
	defer func() {
		s.liveWritePath = ""
		s.liveWriteError = nil
	}()
	s.Equal(s.liveWritePath, virtualPath, "bad write path")
	if s.liveWriteError != nil {
		if _, ok := s.liveWriteError.(ScriptError); !ok {
			return s.liveWriteError
		}
	}
	s.WriteDataFile(virtualPath, content)
	return s.liveWriteError
}

func (s *EditorSuite) expectLiveWrite(path string, err error) {
	s.liveWritePath = path
	s.liveWriteError = err
}

func (s *EditorSuite) verifyLiveWrite() {
	s.Equal("", s.liveWritePath, "LiveWriteDataFile() wasn't called")
}

func (s *EditorSuite) walkSources(walkFn func(virtualPath, physicalPath string)) {
	s.Ck("Walk()", filepath.Walk(s.DataFileTempDir(), func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(s.DataFileTempDir(), path)
		if err != nil {
			return err
		}
		walkFn(relPath, path)
		return nil
	}))
}

func (s *EditorSuite) ListSourceFiles() (entries []LocFileEntry, err error) {
	entries = make([]LocFileEntry, 0)
	s.walkSources(func(virtualPath, physicalPath string) {
		if !strings.HasSuffix(virtualPath, ".js") {
			return
		}

		entry := LocFileEntry{
			VirtualPath:  virtualPath,
			PhysicalPath: physicalPath,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
		}
		if virtualPath == "sample1.js" {
			entry.Devices = []LocItem{{1, "abc"}, {2, "def"}}
			entry.Rules = []LocItem{{10, "foobar"}}
		}
		if s.scriptError != nil && virtualPath == s.scriptErrorPath {
			entry.Error = s.scriptError
		}
		entries = append(entries, entry)
	})
	return
}

func (s *EditorSuite) addSampleFiles() {
	s.WriteDataFile("sample1.js", "// sample1")
	s.WriteDataFile("sample2.js", "// sample2")
}

func (s *EditorSuite) verifySources(expected map[string]string) {
	actual := make(map[string]string)
	s.walkSources(func(virtualPath, physicalPath string) {
		bs, err := ioutil.ReadFile(physicalPath)
		s.Ck("ReadFile()", err)
		actual[virtualPath] = string(bs)
	})
	s.Equal(expected, actual, "sources")
}

func (s *EditorSuite) TestListFiles() {
	s.scriptErrorPath = "sample2.js"
	scriptErr := NewScriptError(
		"syntax error!", []LocItem{
			{1, "sample2.js"},
			{42, "foobar.js"},
		},
	)
	s.scriptError = &scriptErr
	s.VerifyRpc("List", objx.Map{}, []objx.Map{
		{
			"virtualPath": "sample1.js",
			"devices": []objx.Map{
				{"line": 1, "name": "abc"},
				{"line": 2, "name": "def"},
			},
			"rules": []objx.Map{
				{"line": 10, "name": "foobar"},
			},
		},
		{
			"virtualPath": "sample2.js",
			"devices":     []objx.Map{},
			"rules":       []objx.Map{},
			"error": objx.Map{
				"message": "syntax error!",
				"traceback": []objx.Map{
					{"line": 1, "name": "sample2.js"},
					{"line": 42, "name": "foobar.js"},
				},
			},
		},
	})
}

func (s *EditorSuite) verifySave(params, expectedResult objx.Map, err error) {
	s.expectLiveWrite(expectedResult["path"].(string), err)
	s.VerifyRpc("Save", params, expectedResult)
	s.verifyLiveWrite()
}

func (s *EditorSuite) TestSaveFile() {
	s.verifySave(
		objx.Map{"path": "sample1.js", "content": "// sample1 (changed)"},
		objx.Map{"path": "sample1.js"},
		nil,
	)
	s.verifySources(map[string]string{
		"sample1.js": "// sample1 (changed)",
		"sample2.js": "// sample2",
	})
	s.verifySave(
		// make sure spaces are allowed in filenames
		objx.Map{"path": "//sample 3.js", "content": "// sample 3"},
		objx.Map{"path": "sample 3.js"},
		nil,
	)
	s.verifySources(map[string]string{
		"sample1.js":  "// sample1 (changed)",
		"sample2.js":  "// sample2",
		"sample 3.js": "// sample 3",
	})
	s.verifySave(
		objx.Map{"path": "sub/sample4.js", "content": "// sample4"},
		objx.Map{"path": "sub/sample4.js"},
		nil,
	)
	s.verifySave(
		objx.Map{"path": "sub/sample5.js", "content": "sample5 -- error"},
		objx.Map{
			"error": "syntax error!",
			"path":  "sub/sample5.js",
			"traceback": []objx.Map{
				{"line": 1, "name": "sub/sample5.js"},
				{"line": 42, "name": "foobar.js"},
			},
		},
		NewScriptError(
			"syntax error!", []LocItem{
				{1, "sub/sample5.js"},
				{42, "foobar.js"},
			},
		),
	)
	s.verifySources(map[string]string{
		"sample1.js":     "// sample1 (changed)",
		"sample2.js":     "// sample2",
		"sample 3.js":    "// sample 3",
		"sub/sample4.js": "// sample4",
		"sub/sample5.js": "sample5 -- error",
	})

	s.VerifyRpcError("Save", objx.Map{"path": "../foo/bar.js", "content": "evilfile"},
		EDITOR_ERROR_INVALID_PATH, "EditorError", "Invalid path")
	s.VerifyRpcError("Save", objx.Map{"path": "qqq/$$$rrr.js", "content": "lamefile"},
		EDITOR_ERROR_INVALID_PATH, "EditorError", "Invalid path")
	s.verifySources(map[string]string{
		"sample1.js":     "// sample1 (changed)",
		"sample2.js":     "// sample2",
		"sample 3.js":    "// sample 3",
		"sub/sample4.js": "// sample4",
		"sub/sample5.js": "sample5 -- error",
	})
	s.EnsureNoErrorsOrWarnings()

	s.expectLiveWrite("zzz.js", errors.New("fail!"))
	s.VerifyRpcError(
		"Save",
		objx.Map{"path": "zzz.js", "content": "// sample5"},
		EDITOR_ERROR_WRITE, "EditorError",
		"Error writing the file")
	s.EnsureGotErrors()
}

func (s *EditorSuite) TestRemoveFile() {
	s.VerifyRpc("Remove", objx.Map{"path": "sample1.js"}, true)
	s.verifySources(map[string]string{
		"sample2.js": "// sample2",
	})
	s.VerifyRpcError("Remove", objx.Map{"path": "nosuchfile.js"},
		EDITOR_ERROR_FILE_NOT_FOUND, "EditorError", "File not found")
	s.WriteDataFile("unlisted.js.ok", "// unlisted")
	s.VerifyRpcError("Remove", objx.Map{"path": "unlisted.js.ok"},
		EDITOR_ERROR_FILE_NOT_FOUND, "EditorError", "File not found")
	s.verifySources(map[string]string{
		"sample2.js":     "// sample2",
		"unlisted.js.ok": "// unlisted",
	})
}

func (s *EditorSuite) TestLoadFile() {
	s.VerifyRpc("Load", objx.Map{"path": "sample1.js"}, objx.Map{
		"content": "// sample1",
	})
	s.scriptErrorPath = "sample1.js"
	scriptErr := NewScriptError(
		"syntax error!", []LocItem{
			{1, "sub/sample5.js"},
			{42, "foobar.js"},
		},
	)
	s.scriptError = &scriptErr
	s.VerifyRpc("Load", objx.Map{"path": "sample1.js"}, objx.Map{
		"content": "// sample1",
		"error": objx.Map{
			"message": "syntax error!",
			"traceback": []objx.Map{
				{"line": 1, "name": "sub/sample5.js"},
				{"line": 42, "name": "foobar.js"},
			},
		},
	})
	s.VerifyRpcError("Load", objx.Map{"path": "nosuchfile.js"},
		EDITOR_ERROR_FILE_NOT_FOUND, "EditorError", "File not found")
	s.WriteDataFile("unlisted.js.ok", "// unlisted")
	s.VerifyRpcError("Load", objx.Map{"path": "unlisted.js.ok"},
		EDITOR_ERROR_FILE_NOT_FOUND, "EditorError", "File not found")
}

func TestEditorSuite(t *testing.T) {
	testutils.RunSuites(t, new(EditorSuite))
}

// TBD: test trying to overwrite or remove readonly files
// TBD: use verifyMessages()-style formatting for Recorder.Verify() / Recorder.VerifyUnordered()
//      and update tests that use them
