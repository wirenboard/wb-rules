package wbrules

import (
	"fmt"
	wbgo "github.com/contactless/wbgo"
	"github.com/stretchr/objx"
	"io/ioutil"
	"path"
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
	s.rpc.Register(NewEditor(s.scriptDir))
	s.client = s.Broker.MakeClient("tst")
	s.client.Start()
	s.rpc.Start()
	s.Verify(
		"Subscribe -- wbrules: /rpc/v1/wbrules/+/+/+",
		"wbrules -> /rpc/v1/wbrules/Editor/List: [1] (QoS 1, retained)",
	)
}

func (s *EditorSuite) TearDownTest() {
	s.rmScriptDir()
	s.rpc.Stop()
	s.Suite.TearDownTest()
}

func (s *EditorSuite) writeScript(filename, content string) string {
	fullPath := path.Join(s.scriptDir, filename)
	if err := ioutil.WriteFile(fullPath, []byte(content), 0777); err != nil {
		s.Require().Fail("failed to write file", "%s: %s", fullPath, err)
	}
	return fullPath
}

func (s *EditorSuite) addSampleFiles() {
	s.writeScript("sample1.js", "// sample1")
	s.writeScript("sample2.js", "// sample2")
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
	s.verifyRpc(
		subtopic,
		param,
		objx.Map{
			"error": objx.Map{
				"errorMessage": msg,
				"code":         code,
				"data":         typ,
			},
		},
	)
}

func (s *EditorSuite) TestListFiles() {
	s.verifyRpc("List", objx.Map{"path": "/"}, []string{
		"sample1.js",
		"sample2.js",
	})
}

func TestEditorSuite(t *testing.T) {
	wbgo.RunSuites(t, new(EditorSuite))
}

// TBD: make sure "../.." paths don't work
// TBD: list dirs
// TBD: only show .js files and dirs
// TBD: use verifyMessages()-style formatting for Recorder.Verify() / Recorder.VerifyUnordered()
//      and update tests that use them
// TBD: look for safe path handling for Go
