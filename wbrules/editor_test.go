package wbrules

import (
	"fmt"
	"github.com/contactless/wbgo"
	"github.com/stretchr/objx"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

const (
	SAMPLE_CLIENT_ID = "11111111"
)

type EditorSuite struct {
	wbgo.Suite
	*wbgo.FakeMQTTFixture
	client      wbgo.MQTTClient
	rpc         *wbgo.MQTTRPCServer
	scriptDir   string
	rmScriptDir func()
	id          uint64
}

func (s *EditorSuite) SetupTest() {
	s.Suite.SetupTest()
	s.id = 1
	s.scriptDir, s.rmScriptDir = wbgo.SetupTempDir(s.T())
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
		"wbrules -> /rpc/v1/wbrules/Editor/Save: [1] (QoS 1, retained)",
	)
}

func (s *EditorSuite) TearDownTest() {
	s.rmScriptDir()
	s.rpc.Stop()
	s.Suite.TearDownTest()
}

func (s *EditorSuite) ScriptDir() string {
	return s.scriptDir
}

func (s *EditorSuite) walkSources(walkFn func(virtualPath, physicalPath string)) {
	s.Ck("Walk()", filepath.Walk(s.scriptDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(s.scriptDir, path)
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

func (s *EditorSuite) writeScript(filename, content string) string {
	fullPath := filepath.Join(s.scriptDir, filename)
	s.Ck("WriteFile()", ioutil.WriteFile(fullPath, []byte(content), 0777))
	return fullPath
}

func (s *EditorSuite) addSampleFiles() {
	s.writeScript("sample1.js", "// sample1")
	s.writeScript("sample2.js", "// sample2")
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

func (s *EditorSuite) verifyRpcRaw(subtopic string, params objx.Map, expectedResponse objx.Map) {
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

func (s *EditorSuite) verifyRpc(subtopic string, param objx.Map, expectedResult interface{}) {
	s.verifyRpcRaw(subtopic, param, objx.Map{"result": expectedResult})
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

func (s *EditorSuite) TestSaveFile() {
	s.verifyRpc("Save", objx.Map{"path": "sample1.js", "content": "// sample1 (changed)"}, true)
	s.verifySources(map[string]string{
		"sample1.js": "// sample1 (changed)",
		"sample2.js": "// sample2",
	})
	s.verifyRpc("Save", objx.Map{"path": "sample3.js", "content": "// sample3"}, true)
	s.verifySources(map[string]string{
		"sample1.js": "// sample1 (changed)",
		"sample2.js": "// sample2",
		"sample3.js": "// sample3",
	})
	s.verifyRpc("Save", objx.Map{"path": "sub/sample4.js", "content": "// sample4"}, true)
	s.verifySources(map[string]string{
		"sample1.js":     "// sample1 (changed)",
		"sample2.js":     "// sample2",
		"sample3.js":     "// sample3",
		"sub/sample4.js": "// sample4",
	})
	s.verifyRpcError("Save", objx.Map{"path": "../foo/bar.js", "content": "evilfile"},
		EDITOR_ERROR_INVALID_PATH, "EditorError", "Invalid path")
	s.verifyRpcError("Save", objx.Map{"path": "qqq / rrr.js", "content": "lamefile"},
		EDITOR_ERROR_INVALID_PATH, "EditorError", "Invalid path")
}

func TestEditorSuite(t *testing.T) {
	wbgo.RunSuites(t, new(EditorSuite))
}

// TBD: use verifyMessages()-style formatting for Recorder.Verify() / Recorder.VerifyUnordered()
//      and update tests that use them
// TBD: look for safe path handling for Go
