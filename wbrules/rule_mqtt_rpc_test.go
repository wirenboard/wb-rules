package wbrules

import (
	"encoding/json"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

// The MQTT-RPC module (modules/wb-mqtt-rpc.js, the MqttRpc global): the
// client half against a fake server answering on the broker, and the
// server half driven by requests the test publishes itself.
type MqttRpcSuite struct {
	RuleSuiteBase
	// what the fake server saw: request topic -> parsed body
	mu       sync.Mutex
	requests []fakeRpcRequest
}

type fakeRpcRequest struct {
	Topic  string
	ID     json.RawMessage
	Params json.RawMessage
}

var rpcRequestTopicRx = regexp.MustCompile(`^/rpc/v1/([^/]+)/([^/]+)/([^/]+)/([^/]+)$`)

func (s *MqttRpcSuite) SetupTest() {
	s.requests = nil
	s.SetupSkippingDefs("testrules_mqtt_rpc.js")
	// the fake "svc" server: answers M, Err and stays silent on Slow
	s.client.Subscribe(func(msg wbgong.MQTTMessage) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(msg.Payload), &req); err != nil {
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, fakeRpcRequest{msg.Topic, req.ID, req.Params})
		s.mu.Unlock()
		m := rpcRequestTopicRx.FindStringSubmatch(msg.Topic)
		if m == nil {
			return
		}
		var reply string
		switch m[3] {
		case "M", "get_values":
			reply = `{"id":` + string(req.ID) + `,"result":{"echo":` + string(req.Params) + `,"method":"` + m[3] + `"}}`
		case "Err":
			reply = `{"id":` + string(req.ID) + `,"error":{"code":-32602,"message":"bad params","data":"EditorError"}}`
		case "Garbled":
			// what a server answers when it cannot read the request at all
			reply = `{"id":null,"error":{"code":-32700,"message":"Parse error"}}`
		default:
			return
		}
		// deliver from outside the broker's delivery path
		go s.client.Publish(wbgong.MQTTMessage{Topic: msg.Topic + "/reply", Payload: reply, QoS: 1})
	}, "/rpc/v1/svc/S/+/+", "/rpc/v1/db_logger/history/+/+", "/rpc/v1/wb-mqtt-serial/port/+/+")
	s.Verify(
		"Subscribe -- tst: /rpc/v1/svc/S/+/+",
		"Subscribe -- tst: /rpc/v1/db_logger/history/+/+",
		"Subscribe -- tst: /rpc/v1/wb-mqtt-serial/port/+/+",
	)
}

func (s *MqttRpcSuite) lastRequest() fakeRpcRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Require().NotEmpty(s.requests, "no RPC request reached the fake server")
	return s.requests[len(s.requests)-1]
}

// the file's client id, as seen in the last request topic
func (s *MqttRpcSuite) clientID() string {
	m := rpcRequestTopicRx.FindStringSubmatch(s.lastRequest().Topic)
	s.Require().NotNil(m)
	s.Regexp(`^wbrules-[A-Za-z0-9]{10}$`, m[4])
	return m[4]
}

func (s *MqttRpcSuite) TestCallRoundTrip() {
	s.publish("/devices/rpctest/controls/call/on", "1", "rpctest/call")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [call result: {"echo":{"a":1},"method":"M"}] (QoS 1)`)
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [call result 2: {"echo":{},"method":"M"}] (QoS 1)`)
	// the wire format: /rpc/v1/<driver>/<service>/<method>/<clientId> with {id, params}
	req := s.lastRequest()
	m := rpcRequestTopicRx.FindStringSubmatch(req.Topic)
	s.Require().NotNil(m, req.Topic)
	s.Equal("svc", m[1])
	s.Equal("S", m[2])
	s.Equal("M", m[3])
	s.Regexp(`^wbrules-[A-Za-z0-9]{10}$`, m[4])
	s.Equal("2", string(req.ID), "ids count up per file")
	s.Equal("{}", string(req.Params), "omitted params go out as {}")
}

func (s *MqttRpcSuite) TestErrorReply() {
	s.publish("/devices/rpctest/controls/err/on", "1", "rpctest/err")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [err: name=RpcError code=-32602 data="EditorError" rpc=true timeout=false error=true msg=bad params target=svc/S/Err] (QoS 1)`)
}

func (s *MqttRpcSuite) TestTimeout() {
	s.publish("/devices/rpctest/controls/timeout/on", "1", "rpctest/timeout")
	s.SkipTill("new fake timer: 1, 500")
	ts := s.AdvanceTime(500 * time.Millisecond)
	s.FireTimer(1, ts)
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [timeout: name=TimeoutError code=-33000 data="MqttTimeoutError" rpc=true timeout=true error=true msg=no reply from svc/S/Slow in 500 ms target=svc/S/Slow] (QoS 1)`)
	// a reply arriving after the deadline finds nobody waiting and is dropped silently
	req := s.lastRequest()
	s.client.Publish(wbgong.MQTTMessage{Topic: req.Topic + "/reply", Payload: `{"id":` + string(req.ID) + `,"result":1}`, QoS: 1})
	s.Verify("tst -> " + req.Topic + `/reply: [{"id":1,"result":1}] (QoS 1)`)
	s.VerifyEmpty()
}

func (s *MqttRpcSuite) TestNullIdReplySettlesTheOnlyCall() {
	s.publish("/devices/rpctest/controls/nullid/on", "1", "rpctest/nullid")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [nullid: name=RpcError code=-32700 data=undefined rpc=true timeout=false error=true msg=Parse error target=svc/S/Garbled] (QoS 1)`)
}

func (s *MqttRpcSuite) TestCallWaitsForMethod() {
	s.publish("/devices/rpctest/controls/waitcall/on", "1", "rpctest/waitcall")
	// the presence wait is armed, nothing was sent yet
	s.SkipTill("new fake timer: 1, 1000")
	s.client.Publish(wbgong.MQTTMessage{Topic: "/rpc/v1/svc/S/M", Payload: "1", QoS: 1, Retained: true})
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [waitcall result: {"echo":{"after":"presence"},"method":"M"}] (QoS 1)`)
	s.Equal(1, len(s.requests), "exactly one request, sent after the presence arrived")
}

func (s *MqttRpcSuite) TestHasMethod() {
	// the presence topic is retained, so it is there before anybody asks
	s.client.Publish(wbgong.MQTTMessage{Topic: "/rpc/v1/svc/S/M", Payload: "1", QoS: 1, Retained: true})
	s.SkipTill("tst -> /rpc/v1/svc/S/M: [1] (QoS 1, retained)")
	s.publish("/devices/rpctest/controls/has/on", "1", "rpctest/has")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [has M: true] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [has M again: true] (QoS 1)")
	// nothing retained for Nope: false once the wait runs out (timer 1 was
	// the first hasMethod's, cancelled by the retained presence)
	s.SkipTill("new fake timer: 2, 300")
	ts := s.AdvanceTime(300 * time.Millisecond)
	s.FireTimer(2, ts)
	s.SkipTill("wbrules-log -> /wbrules/log/info: [has Nope: false] (QoS 1)")
}

func (s *MqttRpcSuite) TestWaitForMethod() {
	s.publish("/devices/rpctest/controls/wait/on", "1", "rpctest/wait")
	s.SkipTill("new fake timer: 1, 1000")
	// the service comes up: its retained presence resolves the waiter
	s.client.Publish(wbgong.MQTTMessage{Topic: "/rpc/v1/svc/S/Later", Payload: "1", QoS: 1, Retained: true})
	s.SkipTill("wbrules-log -> /wbrules/log/info: [Later available] (QoS 1)")
	s.SkipTill("new fake timer: 2, 200")
	ts := s.AdvanceTime(200 * time.Millisecond)
	s.FireTimer(2, ts)
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [Never: name=TimeoutError code=-33000 data="MqttMethodUnavailable" rpc=true timeout=true error=true msg=svc/S/Never not available after 200 ms target=svc/S/Never] (QoS 1)`)
}

func (s *MqttRpcSuite) TestServiceProxy() {
	s.publish("/devices/rpctest/controls/proxy/on", "1", "rpctest/proxy")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [proxy result: {"echo":{"via":"proxy"},"method":"M"}] (QoS 1)`)
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [proxy call result: {"echo":{"via":"call"},"method":"M"}] (QoS 1)`)
}

func (s *MqttRpcSuite) TestTypedServices() {
	s.publish("/devices/rpctest/controls/typed/on", "1", "rpctest/typed")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [typed result: {"echo":{"channels":[["wb-adc","Vin"]],"limit":1},"method":"get_values"}] (QoS 1)`)
	cid := s.clientID()
	// port/Load carries total_timeout: the client waits for that budget plus a margin
	s.SkipTill("new fake timer: 2, 100000")
	s.SkipTill("wbrules-log -> /rpc/v1/wb-mqtt-serial/port/Load/" + cid + `: [{"id":2,"params":{"path":"/dev/ttyRS485-1","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2,"msg":"0A03008000018499","response_size":8,"total_timeout":90000}}] (QoS 1)`)
}

func (s *MqttRpcSuite) TestRequireAndGlobalAgree() {
	s.publish("/devices/rpctest/controls/same/on", "1", "rpctest/same")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [same instance: true, clientId ok: true] (QoS 1)")
}

func (s *MqttRpcSuite) TestArgumentValidation() {
	s.publish("/devices/rpctest/controls/bad/on", "1", "rpctest/bad")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [bad args rejected: true,true,true,true,true,true] (QoS 1)")
}

// ---- server ----

// publishes a request and returns the recorder line it produces
func (s *MqttRpcSuite) request(topic, payload string) string {
	s.client.Publish(wbgong.MQTTMessage{Topic: topic, Payload: payload, QoS: 1})
	return "tst -> " + topic + ": [" + payload + "] (QoS 1)"
}

// request + the exact reply, in order (both go through the recorder)
func (s *MqttRpcSuite) roundTrip(topic, payload, reply string) {
	sent := s.request(topic, payload)
	s.Verify(sent, "wbrules-log -> "+topic+"/reply: ["+reply+"] (QoS 1)")
}

func (s *MqttRpcSuite) TestServedEcho() {
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `{"id":7,"params":{"x":1}}`,
		`{"id":7,"result":{"params":{"x":1},"method":"Echo","client":"cli1"}}`)
	// string ids are echoed as strings; a missing params object reads as {}
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `{"id":"abc"}`,
		`{"id":"abc","result":{"params":{},"method":"Echo","client":"cli1"}}`)
	// a promise-returning handler answers when it settles; undefined -> null
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Later/cli2", `{"id":1,"params":{"v":21}}`, `{"id":1,"result":42}`)
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Nothing/cli2", `{"id":2,"params":{}}`, `{"id":2,"result":null}`)
	s.roundTrip("/rpc/v1/custom-driver/Other/Ping/cli3", `{"id":3,"params":{}}`, `{"id":3,"result":"pong"}`)
	s.VerifyEmpty()
}

func (s *MqttRpcSuite) TestServedErrors() {
	// RpcError thrown by the handler: its code/data go out verbatim
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Fail/cli1", `{"id":1,"params":{}}`,
		`{"id":1,"error":{"code":1234,"message":"nope","data":{"why":"test"}}}`)
	// any other exception: internal error for the caller, a log line for the author
	sent := s.request("/rpc/v1/wbrules-scripts/Demo/Boom/cli1", `{"id":2,"params":{}}`)
	s.Verify(sent,
		regexp.MustCompile(`^wbrules-log -> /wbrules/log/error: \[MqttRpc: wbrules-scripts/Demo/Boom handler failed: TypeError: bad handler`),
		`wbrules-log -> /rpc/v1/wbrules-scripts/Demo/Boom/cli1/reply: [{"id":2,"error":{"code":-32603,"message":"bad handler","data":"TypeError"}}] (QoS 1)`)
	// protocol errors
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Missing/cli1", `{"id":3,"params":{}}`,
		`{"id":3,"error":{"code":-32601,"message":"unknown method: Missing"}}`)
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `{"params":{}}`,
		`{"id":null,"error":{"code":-32600,"message":"invalid request: no id"}}`)
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `{"id":{"no":1},"params":{}}`,
		`{"id":null,"error":{"code":-32600,"message":"invalid request: bad id"}}`)
	sent = s.request("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `not json`)
	s.Verify(sent, regexp.MustCompile(`^wbrules-log -> /rpc/v1/wbrules-scripts/Demo/Echo/cli1/reply: \[\{"id":null,"error":\{"code":-32700,"message":"parse error: `))
	// a topic with extra levels is not a request: no reply, the next one is served
	extra := s.request("/rpc/v1/wbrules-scripts/Demo/Echo/cli1/extra", `{"id":9,"params":{}}`)
	s.Verify(extra)
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `{"id":10,"params":{}}`,
		`{"id":10,"result":{"params":{},"method":"Echo","client":"cli1"}}`)
	s.VerifyEmpty()
}

func (s *MqttRpcSuite) TestSchemaValidatedMethods() {
	// a valid request reaches the handler with the params as sent
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/SetTarget/cli1", `{"id":1,"params":{"room":"hall","t":22}}`,
		`{"id":1,"result":{"room":"hall","t":22,"mode":"default"}}`)
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/SetTarget/cli1", `{"id":2,"params":{"room":"hall","t":22,"mode":"eco","zones":[1,2]}}`,
		`{"id":2,"result":{"room":"hall","t":22,"mode":"eco"}}`)
	// a bad one is -32602 with every problem in data, the first in the message
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/SetTarget/cli1", `{"id":3,"params":{"room":"","t":40,"mode":"x","zones":[1.5,2,3,4],"extra":1}}`,
		`{"id":3,"error":{"code":-32602,"message":"invalid params: /room must be at least 1 characters","data":[{"path":"/room","message":"must be at least 1 characters"},{"path":"/t","message":"must be <= 35"},{"path":"/mode","message":"must be one of [\"eco\",\"comfort\"]"},{"path":"/zones","message":"must have at most 3 items"},{"path":"/zones/0","message":"must be integer, got number"},{"path":"/extra","message":"is not allowed"}]}}`)
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/SetTarget/cli1", `{"id":4,"params":{"t":"hot"}}`,
		`{"id":4,"error":{"code":-32602,"message":"invalid params: /room is required","data":[{"path":"/room","message":"is required"},{"path":"/t","message":"must be number, got string"}]}}`)
	// the {params, handler} object form validates too; a scalar fails the object schema
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Loose/cli1", `{"id":5,"params":{"a":1,"b":2}}`, `{"id":5,"result":2}`)
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Loose/cli1", `{"id":6,"params":[1]}`,
		`{"id":6,"error":{"code":-32602,"message":"invalid params: / must be object, got array","data":[{"path":"/","message":"must be object, got array"}]}}`)
	// a non-object schema at the root: by-position params
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Union/cli1", `{"id":7,"params":10}`,
		`{"id":7,"error":{"code":-32602,"message":"invalid params: not an object"}}`)
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Union/cli1", `{"id":8,"params":["x"]}`,
		`{"id":8,"error":{"code":-32602,"message":"invalid params: / must match an alternative","data":[{"path":"/","message":"must match an alternative"}]}}`)
}

func (s *MqttRpcSuite) TestValidateFunction() {
	s.publish("/devices/rpctest/controls/validate/on", "1", "rpctest/validate")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [validate: ok | /:must be number, got string | ok | /:must be integer, got number | ok | /:must be one of [1,"a"] | /:must be at most 2 characters | /:must have at least 1 items | /1:must be boolean, got number | /:must match exactly one alternative | /:must not match the excluded schema | /:must be <= 1 | /b:must be number, got string | ok] (QoS 1)`)
}

var servedPresenceTopics = []string{
	"/rpc/v1/wbrules-scripts/Demo/Echo",
	"/rpc/v1/wbrules-scripts/Demo/Fail",
	"/rpc/v1/wbrules-scripts/Demo/Boom",
	"/rpc/v1/wbrules-scripts/Demo/Later",
	"/rpc/v1/wbrules-scripts/Demo/Nothing",
	"/rpc/v1/wbrules-scripts/Demo/Circular",
	"/rpc/v1/wbrules-scripts/Demo/Func",
	"/rpc/v1/wbrules-scripts/Demo/BadData",
	"/rpc/v1/wbrules-scripts/Demo/SetTarget",
	"/rpc/v1/wbrules-scripts/Demo/Loose",
	"/rpc/v1/wbrules-scripts/Demo/Union",
	"/rpc/v1/custom-driver/Other/Ping",
}

func (s *MqttRpcSuite) TestNonSerializableResult() {
	// a circular result cannot go on the wire: the caller gets -32603 (not a
	// timeout) and the author a log line
	sent := s.request("/rpc/v1/wbrules-scripts/Demo/Circular/cli1", `{"id":1,"params":{}}`)
	s.Verify(sent,
		regexp.MustCompile(`^wbrules-log -> /wbrules/log/error: \[MqttRpc: reply on /rpc/v1/wbrules-scripts/Demo/Circular/cli1 is not JSON-serializable: `),
		regexp.MustCompile(`^wbrules-log -> /rpc/v1/wbrules-scripts/Demo/Circular/cli1/reply: \[\{"id":1,"error":\{"code":-32603,"message":"reply is not JSON-serializable: `))
	// an RpcError whose data cannot travel keeps its code and message
	sent = s.request("/rpc/v1/wbrules-scripts/Demo/BadData/cli1", `{"id":9,"params":{}}`)
	s.Verify(sent,
		regexp.MustCompile(`^wbrules-log -> /wbrules/log/error: \[MqttRpc: reply on /rpc/v1/wbrules-scripts/Demo/BadData/cli1 is not JSON-serializable: `),
		`wbrules-log -> /rpc/v1/wbrules-scripts/Demo/BadData/cli1/reply: [{"id":9,"error":{"code":4321,"message":"with unserializable data"}}] (QoS 1)`)
	// a function has no JSON form: null, not a reply without a result member
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Func/cli1", `{"id":2,"params":{}}`, `{"id":2,"result":null}`)
	// by-position params are fine, a scalar is -32602
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `{"id":3,"params":[1,2]}`,
		`{"id":3,"result":{"params":[1,2],"method":"Echo","client":"cli1"}}`)
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `{"id":4,"params":5}`,
		`{"id":4,"error":{"code":-32602,"message":"invalid params: not an object"}}`)
}

func (s *MqttRpcSuite) TestSecondFileCannotServeTheSameService() {
	path := s.CopyDataFileToTempDir("testrules_mqtt_rpc_dup.js", "testrules_mqtt_rpc_dup.js")
	err := s.engine.LiveLoadFile(path)
	s.Require().Error(err, "the second file must fail to load")
	s.Contains(err.Error(), "wbrules-scripts/Demo is already served by another rule file")
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_mqtt_rpc_dup.js] (QoS 1)")
	s.Contains(loadedEntryError(s.T(), s.engine, path), "wbrules-scripts/Demo is already served by another rule file")
	// the first file still answers, and only once
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `{"id":1,"params":{"x":1}}`,
		`{"id":1,"result":{"params":{"x":1},"method":"Echo","client":"cli1"}}`)
	s.VerifyEmpty()
}

func (s *MqttRpcSuite) TestRedefinitionInTheSameFile() {
	s.publish("/devices/rpctest/controls/redefine/on", "1", "rpctest/redefine")
	s.SkipTill("wbrules-log -> /wbrules/log/warning: [MqttRpc: wbrules-scripts/Demo/Nothing redefined] (QoS 1)")
	s.SkipTill("wbrules-log -> /rpc/v1/wbrules-scripts/Demo/Nothing: [1] (QoS 1, retained)")
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Nothing/cli2", `{"id":1,"params":{}}`, `{"id":1,"result":"replaced"}`)
}

func (s *MqttRpcSuite) TestInfiniteTimeout() {
	s.publish("/devices/rpctest/controls/forever/on", "1", "rpctest/forever")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [forever result: {"echo":{"limit":"none"},"method":"M"}] (QoS 1)`)
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [forever result 2: {"echo":{"limit":"default"},"method":"M"}] (QoS 1)`)
	s.VerifyEmpty() // in particular: no timer was created for either call
}

// The -cleanup flag (RunAllCleanups on engine stop) now runs JS unload
// hooks: they must execute on the engine loop, and the served methods'
// presence must be cleared by the stop.
type MqttRpcCleanupOnStopSuite struct {
	RuleSuiteBase
}

func (s *MqttRpcCleanupOnStopSuite) SetupTest() {
	s.CleanupOnStop = true
	s.SetupSkippingDefs("testrules_mqtt_rpc.js")
}

func (s *MqttRpcCleanupOnStopSuite) TestPresenceClearedOnStop() {
	// a request in flight through a served async handler while stopping
	s.client.Publish(wbgong.MQTTMessage{Topic: "/rpc/v1/wbrules-scripts/Demo/Later/cli1", Payload: `{"id":1,"params":{"v":1}}`, QoS: 1})
	s.SkipTill(`wbrules-log -> /rpc/v1/wbrules-scripts/Demo/Later/cli1/reply: [{"id":1,"result":2}] (QoS 1)`)
	s.engine.Stop()
	for _, topic := range servedPresenceTopics {
		s.SkipTill("wbrules-log -> " + topic + ": [] (QoS 1, retained)")
	}
	// the stop-time cleanups (unsubscribes, virtual device removal) were all
	// issued before Stop returned: a sentinel published now drains them
	s.client.Publish(wbgong.MQTTMessage{Topic: "/test/stopped", Payload: "1", QoS: 1})
	s.SkipTill("tst -> /test/stopped: [1] (QoS 1)")
}

func (s *MqttRpcSuite) TestPresenceFollowsTheFile() {
	// unloading the file clears every presence it announced (retained "")
	s.RemoveScript("testrules_mqtt_rpc.js")
	for _, topic := range servedPresenceTopics {
		s.SkipTill("wbrules-log -> " + topic + ": [] (QoS 1, retained)")
	}
	s.SkipTill("wbrules-log -> /wbrules/updates/removed: [testrules_mqtt_rpc.js] (QoS 1)")
	// nobody answers any more
	sent := s.request("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `{"id":1,"params":{}}`)
	s.Verify(sent)
	s.VerifyEmpty()
	// loading it again announces them again (retained "1"), in definition order
	s.Ck("LiveLoadScript", s.LiveLoadScript("testrules_mqtt_rpc.js"))
	for _, topic := range servedPresenceTopics {
		s.SkipTill("wbrules-log -> " + topic + ": [1] (QoS 1, retained)")
	}
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_mqtt_rpc.js] (QoS 1)")
	s.roundTrip("/rpc/v1/wbrules-scripts/Demo/Echo/cli1", `{"id":2,"params":{}}`,
		`{"id":2,"result":{"params":{},"method":"Echo","client":"cli1"}}`)
}

func TestMqttRpc(t *testing.T) {
	testutils.RunSuites(t, new(MqttRpcSuite), new(MqttRpcCleanupOnStopSuite))
}
