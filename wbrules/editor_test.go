package wbrules

import (
	"errors"
	"fmt"
	"github.com/contactless/wbgo"
	"github.com/stretchr/objx"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	SAMPLE_CLIENT_ID = "11111111"
)

type EditorSuite struct {
	wbgo.Suite
	*wbgo.FakeMQTTFixture
	*ScriptFixture
	client         wbgo.MQTTClient
	rpc            *wbgo.MQTTRPCServer
	id             uint64
	liveWritePath  string
	liveWriteError error
}

func (s *EditorSuite) T() *testing.T {
	return s.Suite.T()
}

func (s *EditorSuite) SetupTest() {
	s.Suite.SetupTest()
	s.id = 1
	s.liveWritePath = ""
	s.liveWriteError = nil
	s.ScriptFixture = NewScriptFixture(s.T())
	s.addSampleFiles()
	s.FakeMQTTFixture = wbgo.NewFakeMQTTFixture(s.T())
	s.rpc = wbgo.NewMQTTRPCServer("wbrules", s.Broker.MakeClient("wbrules"))
	s.rpc.Register(NewEditor(s))
	s.client = s.Broker.MakeClient("tst")
	s.client.Start()
	s.rpc.Start()
	s.Verify(
		"Subscribe -- wbrules: /rpc/v1/wbrules/+/+/+",
		"wbrules -> /rpc/v1/wbrules/Editor/List: [1] (QoS 1, retained)",
		"wbrules -> /rpc/v1/wbrules/Editor/Load: [1] (QoS 1, retained)",
		"wbrules -> /rpc/v1/wbrules/Editor/Remove: [1] (QoS 1, retained)",
		"wbrules -> /rpc/v1/wbrules/Editor/Save: [1] (QoS 1, retained)",
	)
}

func (s *EditorSuite) TearDownTest() {
	s.TearDownScripts()
	s.rpc.Stop()
	s.Suite.TearDownTest()
}

func (s *EditorSuite) ScriptDir() string {
	return s.ScriptTmpDir
}

func (s *EditorSuite) LiveWriteScript(virtualPath, content string) error {
	if s.liveWritePath == "" {
		s.Require().Fail("unexpected LiveWriteScript()")
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
	s.WriteScript(virtualPath, content)
	return s.liveWriteError
}

func (s *EditorSuite) expectLiveWrite(path string, err error) {
	s.liveWritePath = path
	s.liveWriteError = err
}

func (s *EditorSuite) verifyLiveWrite() {
	s.Equal("", s.liveWritePath, "LiveWriteScript() wasn't called")
}

func (s *EditorSuite) walkSources(walkFn func(virtualPath, physicalPath string)) {
	s.Ck("Walk()", filepath.Walk(s.ScriptTmpDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(s.ScriptTmpDir, path)
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
		entries = append(entries, entry)
	})
	return
}

func (s *EditorSuite) addSampleFiles() {
	s.WriteScript("sample1.js", "// sample1")
	s.WriteScript("sample2.js", "// sample2")
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

func (s *EditorSuite) verifyRpcRaw(subtopic string, params, expectedResponse objx.Map) {
	replyId := strconv.FormatUint(s.id, 10)
	request := objx.Map{
		"id":     replyId,
		"params": params,
	}
	s.id++
	topic := fmt.Sprintf("/rpc/v1/wbrules/Editor/%s/%s", subtopic, SAMPLE_CLIENT_ID)
	payload := request.MustJSON()
	s.client.Publish(wbgo.MQTTMessage{topic, payload, 1, false})
	resp := expectedResponse.Copy()
	resp["id"] = replyId
	s.Verify(
		fmt.Sprintf("tst -> %s: [%s] (QoS 1)", topic, payload),
		fmt.Sprintf("wbrules -> %s/reply: [%s] (QoS 1)", topic, resp.MustJSON()),
	)
}

func (s *EditorSuite) verifyRpc(subtopic string, params objx.Map, expectedResult interface{}) {
	s.verifyRpcRaw(subtopic, params, objx.Map{"result": expectedResult})
}

func (s *EditorSuite) verifyRpcError(subtopic string, param objx.Map, code int, typ string, msg string) {
	s.verifyRpcRaw(
		subtopic,
		param,
		objx.Map{
			"error": objx.Map{
				"message": msg,
				"code":    code,
				"data":    typ,
			},
		},
	)
}

func (s *EditorSuite) TestListFiles() {
	s.verifyRpc("List", objx.Map{}, []objx.Map{
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
		},
	})
}

func (s *EditorSuite) verifySave(params, expectedResult objx.Map, err error) {
	s.expectLiveWrite(expectedResult["path"].(string), err)
	s.verifyRpc("Save", params, expectedResult)
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
		objx.Map{"path": "//sample3.js", "content": "// sample3"},
		objx.Map{"path": "sample3.js"},
		nil,
	)
	s.verifySources(map[string]string{
		"sample1.js": "// sample1 (changed)",
		"sample2.js": "// sample2",
		"sample3.js": "// sample3",
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
		"sample3.js":     "// sample3",
		"sub/sample4.js": "// sample4",
		"sub/sample5.js": "sample5 -- error",
	})

	s.verifyRpcError("Save", objx.Map{"path": "../foo/bar.js", "content": "evilfile"},
		EDITOR_ERROR_INVALID_PATH, "EditorError", "Invalid path")
	s.verifyRpcError("Save", objx.Map{"path": "qqq / rrr.js", "content": "lamefile"},
		EDITOR_ERROR_INVALID_PATH, "EditorError", "Invalid path")
	s.verifySources(map[string]string{
		"sample1.js":     "// sample1 (changed)",
		"sample2.js":     "// sample2",
		"sample3.js":     "// sample3",
		"sub/sample4.js": "// sample4",
		"sub/sample5.js": "sample5 -- error",
	})
	s.EnsureNoErrorsOrWarnings()

	s.expectLiveWrite("zzz.js", errors.New("fail!"))
	s.verifyRpcError(
		"Save",
		objx.Map{"path": "zzz.js", "content": "// sample5"},
		EDITOR_ERROR_WRITE, "EditorError",
		"Error writing the file")
	s.EnsureGotErrors()
}

func (s *EditorSuite) TestRemoveFile() {
	s.verifyRpc("Remove", objx.Map{"path": "sample1.js"}, true)
	s.verifySources(map[string]string{
		"sample2.js": "// sample2",
	})
	s.verifyRpcError("Remove", objx.Map{"path": "nosuchfile.js"},
		EDITOR_ERROR_FILE_NOT_FOUND, "EditorError", "File not found")
	s.WriteScript("unlisted.js.ok", "// unlisted")
	s.verifyRpcError("Remove", objx.Map{"path": "unlisted.js.ok"},
		EDITOR_ERROR_FILE_NOT_FOUND, "EditorError", "File not found")
	s.verifySources(map[string]string{
		"sample2.js":     "// sample2",
		"unlisted.js.ok": "// unlisted",
	})
}

func (s *EditorSuite) TestLoadFile() {
	s.verifyRpc("Load", objx.Map{"path": "sample1.js"}, objx.Map{
		"content": "// sample1",
	})
	s.verifyRpcError("Load", objx.Map{"path": "nosuchfile.js"},
		EDITOR_ERROR_FILE_NOT_FOUND, "EditorError", "File not found")
	s.WriteScript("unlisted.js.ok", "// unlisted")
	s.verifyRpcError("Load", objx.Map{"path": "unlisted.js.ok"},
		EDITOR_ERROR_FILE_NOT_FOUND, "EditorError", "File not found")
}

func TestEditorSuite(t *testing.T) {
	wbgo.RunSuites(t, new(EditorSuite))
}

// TBD: test trying to overwrite or remove readonly files
// TBD: use verifyMessages()-style formatting for Recorder.Verify() / Recorder.VerifyUnordered()
//      and update tests that use them
