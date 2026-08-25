package wbrules

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

// The friendly layer of the MQTT-RPC module: a fake wb-mqtt-serial /
// wb-mqtt-db / wb-mqtt-logs / wbrules / confed / wb-device-manager /
// wb-diag-collect on the test broker answers the raw methods, the rule
// file exercises the helpers on top and logs what it got.
type MqttRpcHelpersSuite struct {
	RuleSuiteBase
	mu       sync.Mutex
	requests []fakeRpcRequest
	// fake timers by duration, in creation order (the helpers' waits)
	timers map[time.Duration][]TimerId
	// the fake editor's file list: name -> enabled
	files map[string]bool
}

func (s *MqttRpcHelpersSuite) SetupTest() {
	s.requests = nil
	s.timers = make(map[time.Duration][]TimerId)
	s.files = map[string]bool{"a.js": true}
	s.SetupSkippingDefs("testrules_mqtt_rpc_helpers.js")
	// remember every fake timer by duration: a helper's wait is fired by
	// what it waits for, not by an id that depends on message timing
	s.engine.SetTimerFunc(func(id TimerId, d time.Duration, periodic bool) wbgong.Timer {
		s.mu.Lock()
		s.timers[d] = append(s.timers[d], id)
		s.mu.Unlock()
		return s.newFakeTimer(id, d, periodic)
	})
	s.client.Subscribe(s.fakeServer, "/rpc/v1/+/+/+/+")
	s.Verify("Subscribe -- tst: /rpc/v1/+/+/+/+")
	// the firmware state as a controller has it before any update: a failure
	// left from an earlier attempt of slave 5, an old error of slave 6
	s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[{"port":{"path":"/dev/ttyRS485-1"},"slave_id":5,"progress":30,"type":"firmware","error":{"message":"old failure"}},{"port":{"path":"/dev/ttyRS485-1"},"slave_id":6,"progress":0,"type":"firmware","error":{"message":"recorded error"}}]}`)
	s.Verify(`tst -> /wb-mqtt-serial/firmware_update/state: [{"devices":[{"port":{"path":"/dev/ttyRS485-1"},"slave_id":5,"progress":30,"type":"firmware","error":{"message":"old failure"}},{"port":{"path":"/dev/ttyRS485-1"},"slave_id":6,"progress":0,"type":"firmware","error":{"message":"recorded error"}}]}] (QoS 1, retained)`)
}

func (s *MqttRpcHelpersSuite) reply(topic, body string) {
	go s.client.Publish(wbgong.MQTTMessage{Topic: topic + "/reply", Payload: body, QoS: 1})
}

// retained publishes synchronously: call it from a goroutine of your own
// when inside the broker's delivery path
func (s *MqttRpcHelpersSuite) retained(topic, body string) {
	s.client.Publish(wbgong.MQTTMessage{Topic: topic, Payload: body, QoS: 1, Retained: true})
}

// fakeServer answers like the real services would, for the requests the
// helpers under test produce
func (s *MqttRpcHelpersSuite) fakeServer(msg wbgong.MQTTMessage) {
	m := rpcRequestTopicRx.FindStringSubmatch(msg.Topic)
	if m == nil || m[4] == "cli9" {
		return
	}
	var req struct {
		ID     json.RawMessage `json:"id"`
		Params map[string]any  `json:"params"`
	}
	if err := json.Unmarshal([]byte(msg.Payload), &req); err != nil {
		return
	}
	raw, _ := json.Marshal(req.Params)
	s.mu.Lock()
	s.requests = append(s.requests, fakeRpcRequest{msg.Topic, req.ID, raw})
	s.mu.Unlock()
	id := string(req.ID)
	ok := func(result string) { s.reply(msg.Topic, `{"id":`+id+`,"result":`+result+`}`) }
	p := req.Params
	switch m[1] + "/" + m[2] + "/" + m[3] {
	case "wb-mqtt-serial/port/Load":
		if p["protocol"] == "raw" {
			ok(`{"response":"1605000aff00af1f"}`)
			return
		}
		fn, _ := p["function"].(float64)
		addr, _ := p["address"].(float64)
		count := 1.0
		if c, has := p["count"].(float64); has {
			count = c
		}
		switch {
		case addr == 0x9999:
			ok(`{"exception":{"code":2,"msg":"Illegal data address"}}`)
		case fn == 3 || fn == 4:
			// registers 0x1234, 0x0001, ... as many as asked
			hex := ""
			for i := 0; i < int(count); i++ {
				hex += []string{"1234", "0001", "ffff"}[i%3]
			}
			ok(`{"response":"` + hex + `"}`)
		case fn == 1 || fn == 2:
			ok(`{"response":"0502"}`) // bits: 1,0,1,0,0,0,0,0 | 0,1
		default:
			ok(`{"response":""}`)
		}
	case "wb-mqtt-serial/config/Load":
		ok(`{"config":{"ports":[{"path":"/dev/ttyRS485-1","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2,"devices":[{"device_type":"WB-MAP12E","slave_id":1},{"device_type":"WB-MR6C","slave_id":"12","id":"relays","name":"Relays","enabled":false}]},{"address":"10.0.0.5","port":502,"devices":[{"device_type":"WB-MR6C","slave_id":3}]}]},"schema":{},"types":[{"name":"Wiren Board","types":[{"name":"WB-MAP12E","type":"WB-MAP12E","deprecated":false,"protocol":"modbus","mqtt-id":"wb-map12e"},{"name":"WB-MR6C","type":"WB-MR6C","deprecated":false,"protocol":"modbus","mqtt-id":"wb-mr6c"}]}]}`)
	case "wb-mqtt-serial/ports/Load":
		ok(`[{"path":"/dev/ttyRS485-1","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2},{"address":"10.0.0.5","port":502}]`)
	case "wb-mqtt-serial/fw-update/GetFirmwareInfo":
		ok(`{"fw":"1.2.3","available_fw":"1.3.0","can_update":true,"fw_has_update":true,"bootloader":"1.0","available_bootloader":"1.0","bootloader_has_update":false,"model":"WB-MAP12E","components":{}}`)
	case "wb-mqtt-serial/fw-update/Update":
		ok(`"Ok"`)
		port, _ := p["port"].(map[string]any)
		slave := p["slave_id"].(float64)
		// the state topic lists devices as {"port": {"path": ...}, ...}
		if slave == 4 && p["type"] == nil {
			go s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[{"port":{"path":"/dev/ttyRS485-1"},"slave_id":4,"progress":10,"type":"firmware","error":{"message":"device not responding"}}]}`)
			return
		}
		if slave == 4 {
			// a retry while the old error is still listed: a fresh entry, then success
			go func() {
				s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[{"port":{"path":"/dev/ttyRS485-1"},"slave_id":4,"progress":0,"type":"bootloader","error":null}]}`)
				time.Sleep(20 * time.Millisecond)
				s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[]}`)
			}()
			return
		}
		if slave == 5 {
			// the file's first firmware call, with a stale failure retained for
			// this very device: a fresh entry, then the components stage under
			// a new entry, then done
			go func() {
				s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[{"port":{"path":"/dev/ttyRS485-1"},"slave_id":5,"progress":0,"type":"firmware","error":null},{"port":{"path":"/dev/ttyRS485-1"},"slave_id":6,"progress":0,"type":"firmware","error":{"message":"recorded error"}}]}`)
				time.Sleep(20 * time.Millisecond)
				s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[{"port":{"path":"/dev/ttyRS485-1"},"slave_id":6,"progress":0,"type":"firmware","error":{"message":"recorded error"}}]}`)
				time.Sleep(20 * time.Millisecond)
				s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[{"port":{"path":"/dev/ttyRS485-1"},"slave_id":5,"progress":40,"type":"component","error":null},{"port":{"path":"/dev/ttyRS485-1"},"slave_id":6,"progress":0,"type":"firmware","error":{"message":"recorded error"}}]}`)
				time.Sleep(20 * time.Millisecond)
				s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[{"port":{"path":"/dev/ttyRS485-1"},"slave_id":6,"progress":0,"type":"firmware","error":{"message":"recorded error"}}]}`)
			}()
			return
		}
		// progress 0 -> 50 -> done (entry gone), in order
		go func() {
			s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[{"port":{"path":"`+port["path"].(string)+`"},"slave_id":3,"progress":0,"type":"firmware","error":null}]}`)
			time.Sleep(20 * time.Millisecond)
			s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[{"port":{"path":"/dev/ttyRS485-1"},"slave_id":3,"progress":50,"type":"firmware","error":null}]}`)
			time.Sleep(20 * time.Millisecond)
			s.retained("/wb-mqtt-serial/firmware_update/state", `{"devices":[]}`)
		}()
	case "wb-device-manager/fw-update/ClearError", "wb-device-manager/fw-update/GetFirmwareInfo":
		ok(`"Ok"`)
	case "wb-mqtt-serial/device/LoadConfig":
		ok(`{"parameters":{"baud_rate":96,"in1_mode":1},"fw":"1.2.3","model":"WB-MAP12E"}`)
	case "wb-mqtt-serial/device/Load":
		if p["parameters"] != nil {
			ok(`{"channels":{},"parameters":{"baud_rate":96},"readonly":[]}`)
		} else {
			ok(`{"channels":{"Urms L1":230.1,"Irms L1":0.5},"parameters":{},"readonly":["Urms L1"]}`)
		}
	case "wb-mqtt-serial/device/Set", "wb-mqtt-serial/device/SetPoll", "wb-mqtt-serial/port/Setup":
		ok(`{}`)
	case "wb-mqtt-serial/device/Probe":
		if p["slave_id"].(float64) == 8 {
			ok(`{}`)
		} else {
			ok(`{"sn":"4265607","device_signature":"WB-MR6C","cfg":{"slave_id":7,"data_bits":8,"baud_rate":115200}}`)
		}
	case "wb-mqtt-serial/port/Scan":
		if p["path"] == "/dev/ttyRS485-3" {
			ok(`{"devices":[{"sn":"9"}],"error":"port busy"}`)
		} else {
			ok(`{"devices":[{"sn":"1"},{"sn":"2"}]}`)
		}
	case "db_logger/history/get_channels":
		ok(`{"channels":{"wb-adc/Vin":{"items":100,"last_ts":1756000000},"wb-adc/A1":{"items":3,"last_ts":1755990000}}}`)
	case "db_logger/history/get_values":
		chans := p["channels"].([]any)
		pair := chans[0].([]any)
		if pair[1] == "A1" {
			ok(`{"values":[{"i":7,"c":2,"t":1755990000.5,"v":"1.5","retain":false}]}`)
		} else if p["max_records"] != nil {
			// the database averages per epoch-aligned bucket: a window usually spans two
			ok(`{"values":[{"i":8,"c":1,"t":1755999990,"v":"24.0","min":"23.9","max":"24.1","retain":false},{"i":9,"c":1,"t":1756000000,"v":"24.5","min":"24.4","max":"24.6","retain":false}]}`)
		} else {
			ok(`{"values":[{"i":8,"c":1,"t":1755999999.25,"v":"24.3","retain":false},{"i":9,"c":1,"t":1756000000,"v":"24.4","retain":true}],"has_more":true}`)
		}
	case "wbrules/Editor/List":
		s.mu.Lock()
		list := ""
		for name, enabled := range s.files {
			if list != "" {
				list += ","
			}
			list += fmt.Sprintf(`{"virtualPath":%q,"enabled":%v,"rules":[],"devices":[],"timers":[]}`, name, enabled)
		}
		s.mu.Unlock()
		ok("[" + list + "]")
	case "wbrules/Editor/Load":
		ok(`{"content":"// a","enabled":true}`)
	case "wbrules/Editor/Save":
		ok(`{"path":"a.js"}`)
	case "wbrules/Editor/Remove":
		s.mu.Lock()
		delete(s.files, p["path"].(string))
		s.mu.Unlock()
		ok(`true`)
	case "wbrules/Editor/Rename":
		s.mu.Lock()
		s.files[p["new_path"].(string)] = s.files[p["path"].(string)]
		delete(s.files, p["path"].(string))
		s.mu.Unlock()
		ok(`true`)
	case "wbrules/Editor/ChangeState":
		s.mu.Lock()
		s.files[p["path"].(string)] = p["state"].(bool)
		s.mu.Unlock()
		ok(`true`)
	case "wbrules/Editor/Check":
		s.mu.Lock()
		n := 0
		for _, r := range s.requests {
			if strings.HasPrefix(r.Topic, "/rpc/v1/wbrules/Editor/Check/") {
				n++
			}
		}
		s.mu.Unlock()
		if n < 2 {
			ok(`{"status":"pending","diags":[]}`)
		} else {
			ok(`{"status":"ready","diags":[{"line":1,"column":1,"severity":"warning","message":"x"}]}`)
		}
	case "wbrules/Editor/GetTypes":
		ok(`{"content":"declare const x: number;"}`)
	case "confed/Editor/List":
		ok(`[{"title":"X","description":"","configPath":"/etc/x.conf","schemaPath":"/usr/share/x.schema.json","editor":"x"}]`)
	case "confed/Editor/Load":
		ok(`{"configPath":"/etc/x.conf","content":{"debug":false,"n":1},"schema":{},"editor":"x"}`)
	case "confed/Editor/Save":
		ok(`{"path":"/etc/x.conf"}`)
	case "wb_logs/logs/List":
		ok(`{"boots":[{"hash":"b1","start":1755000000,"end":1755100000}],"services":["wb-rules.service","wb-mqtt-serial.service"]}`)
	case "wb_logs/logs/Load":
		ok(`[{"time":1756000000123,"msg":"second","cursor":"c2"},{"time":1755999999000,"level":3,"msg":"first","service":"wb-rules.service","cursor":"c1"}]`)
	case "wb-device-manager/bus-scan/Start":
		ok(`"Ok"`)
		go func() {
			s.retained("/wb-device-manager/state", `{"scanning":true,"progress":10,"devices":[]}`)
			time.Sleep(20 * time.Millisecond)
			s.retained("/wb-device-manager/state", `{"scanning":true,"progress":60,"devices":[{"sn":"11"}]}`)
			time.Sleep(20 * time.Millisecond)
			s.retained("/wb-device-manager/state", `{"scanning":false,"progress":100,"devices":[{"sn":"11"},{"sn":"22"}],"error":null}`)
		}()
	case "diag/main/diag":
		ok(`"Ok"`)
		go func() {
			time.Sleep(30 * time.Millisecond)
			s.client.Publish(wbgong.MQTTMessage{Topic: "/wb-diag-collect/artifact", Payload: `{"basename":"diag.zip","fullname":"/var/www/diag/diag.zip"}`, QoS: 1})
		}()
	default:
		s.reply(msg.Topic, `{"id":`+id+`,"error":{"code":-32601,"message":"fake server: unknown method"}}`)
	}
}

// fireNth fires the n-th (1-based) fake timer of the given duration once it
// exists; the helpers' waits are the only timers of these durations
func (s *MqttRpcHelpersSuite) fireNth(d time.Duration, n int) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		ids := s.timers[d]
		s.mu.Unlock()
		if len(ids) >= n {
			s.FireTimer(uint64(ids[n-1]), s.AdvanceTime(d))
			return
		}
		if time.Now().After(deadline) {
			s.FailNowf("timer", "no %d-th fake timer of %s", n, d)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *MqttRpcHelpersSuite) sent(driver, service, method string) []fakeRpcRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []fakeRpcRequest
	for _, r := range s.requests {
		if strings.HasPrefix(r.Topic, "/rpc/v1/"+driver+"/"+service+"/"+method+"/") {
			out = append(out, r)
		}
	}
	return out
}

func (s *MqttRpcHelpersSuite) params(driver, service, method string, i int) map[string]any {
	reqs := s.sent(driver, service, method)
	s.Require().Greater(len(reqs), i, "%s/%s/%s request #%d", driver, service, method, i)
	var p map[string]any
	s.Ck("params", json.Unmarshal(reqs[i].Params, &p))
	return p
}

func (s *MqttRpcHelpersSuite) trigger(cell string) {
	s.publish("/devices/rpchelp/controls/"+cell+"/on", "1", "rpchelp/"+cell)
}

func (s *MqttRpcHelpersSuite) logged(line string) {
	s.SkipTill("wbrules-log -> /wbrules/log/info: [" + line + "] (QoS 1)")
}

func (s *MqttRpcHelpersSuite) TestModbusByDeviceId() {
	s.trigger("modbus")
	// what went on the wire: device_id + Modbus header, hex data, no port fields
	s.logged("modbus checks: true,true,true,true,true")
	loads := s.sent("wb-mqtt-serial", "port", "Load")
	s.Require().Len(loads, 8)
	s.JSONEq(`{"device_id":"wb-map12e_1","function":3,"address":128,"count":2,"format":"HEX"}`, string(loads[0].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","function":4,"address":16,"count":1,"format":"HEX"}`, string(loads[1].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","function":1,"address":0,"count":10,"format":"HEX"}`, string(loads[2].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","function":6,"address":32,"msg":"1234","format":"HEX"}`, string(loads[3].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","function":16,"address":33,"count":2,"msg":"0001ffff","format":"HEX"}`, string(loads[4].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","function":5,"address":5,"msg":"ff00","format":"HEX"}`, string(loads[5].Params))
	// 9 coils: 1,0,0,1,1,0,0,0 | 1 -> 0x19, 0x01
	s.JSONEq(`{"device_id":"wb-map12e_1","function":15,"address":6,"count":9,"msg":"1901","format":"HEX"}`, string(loads[6].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","function":3,"address":39321,"count":1,"format":"HEX"}`, string(loads[7].Params))
}

func (s *MqttRpcHelpersSuite) TestModbusResultsDecoded() {
	s.trigger("modbus")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [holding: [4660,1]] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [input: [4660]] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [coils: [true,false,true,false,false,false,false,false,false,true]] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [writes done] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [exception: ModbusError code=2 modbus=true] (QoS 1)")
	s.logged("modbus checks: true,true,true,true,true")
}

func (s *MqttRpcHelpersSuite) TestModbusOnPorts() {
	s.trigger("modbusPort")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [port holding: [4660,1]] (QoS 1)")
	s.logged("setup done")
	loads := s.sent("wb-mqtt-serial", "port", "Load")
	s.Require().Len(loads, 5)
	// a bare path gets the 9600 N 8 2 defaults; the budget flows into total_timeout
	s.JSONEq(`{"path":"/dev/ttyRS485-2","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2,"protocol":"modbus","slave_id":7,"function":3,"address":128,"count":2,"format":"HEX","total_timeout":3000}`, string(loads[0].Params))
	// "host:port" is Modbus TCP; rtuOverTcp keeps RTU frames over the socket
	s.JSONEq(`{"ip":"10.0.0.5","port":502,"protocol":"modbus-tcp","slave_id":1,"function":4,"address":1,"count":1,"format":"HEX"}`, string(loads[1].Params))
	s.JSONEq(`{"ip":"10.0.0.6","port":502,"protocol":"modbus","slave_id":2,"function":4,"address":1,"count":1,"format":"HEX"}`, string(loads[2].Params))
	s.JSONEq(`{"path":"/dev/ttyRS485-2","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2,"protocol":"raw","msg":"0a03008000018499","response_size":8,"format":"HEX"}`, string(loads[3].Params))
	// function 23 carries the write side
	s.JSONEq(`{"path":"/dev/ttyRS485-2","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2,"protocol":"modbus","slave_id":7,"function":23,"address":16,"count":2,"write_address":32,"write_count":1,"msg":"1234","format":"HEX"}`, string(loads[4].Params))
	// a Modbus TCP device: settings say modbus_mode TCP, the probe modbus-tcp; RTU over TCP stays modbus
	s.JSONEq(`{"ip":"10.0.0.5","port":502,"slave_id":1,"device_type":"WB-MR6C","modbus_mode":"TCP"}`, string(s.sent("wb-mqtt-serial", "device", "LoadConfig")[0].Params))
	probes := s.sent("wb-mqtt-serial", "device", "Probe")
	s.Require().Len(probes, 4)
	s.JSONEq(`{"ip":"10.0.0.5","port":502,"slave_id":1,"protocol":"modbus-tcp"}`, string(probes[0].Params))
	s.JSONEq(`{"ip":"10.0.0.6","port":502,"slave_id":2,"protocol":"modbus"}`, string(probes[1].Params))
	s.JSONEq(`{"path":"/dev/ttyRS485-2","baud_rate":115200,"parity":"N","data_bits":8,"stop_bits":2,"slave_id":7}`, string(probes[2].Params))
	s.JSONEq(`{"path":"/dev/ttyRS485-2","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2,"mode":"all"}`, string(s.sent("wb-mqtt-serial", "port", "Scan")[0].Params))
	// setup items: sn addressing, the new parity as the driver's number code
	s.JSONEq(`{"path":"/dev/ttyRS485-2","items":[{"sn":4265607,"cfg":{"slave_id":12,"parity":2}}]}`, string(s.sent("wb-mqtt-serial", "port", "Setup")[0].Params))
}

func (s *MqttRpcHelpersSuite) TestPortResultsDecoded() {
	s.trigger("modbusPort")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [raw: 1605000aff00af1f] (QoS 1)")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [probe: {"sn":"4265607","device_signature":"WB-MR6C","cfg":{"slave_id":7,"data_bits":8,"baud_rate":115200}}] (QoS 1)`)
	s.SkipTill("wbrules-log -> /wbrules/log/info: [probe empty: null] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [scan: 2 devices] (QoS 1)")
	s.logged("scan error: scan of /dev/ttyRS485-3 failed: port busy partial=1")
	s.logged("setup done")
}

func (s *MqttRpcHelpersSuite) TestDeviceList() {
	s.trigger("devices")
	// ids: explicit "id" wins, else "<mqtt-id>_<slave_id>"; disabled follows the device
	s.SkipTill("wbrules-log -> /wbrules/log/info: [devices: wb-map12e_1:WB-MAP12E:1:number:/dev/ttyRS485-1:true relays:WB-MR6C:12:number:/dev/ttyRS485-1:false wb-mr6c_3:WB-MR6C:3:number:10.0.0.5:true] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [types: WB-MAP12E@Wiren Board WB-MR6C@Wiren Board] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [ports: 2] (QoS 1)")
	// a device id resolves to its port + numeric slave id for the firmware service
	s.logged("fw: 1.2.3 update=true")
	s.JSONEq(`{"slave_id":1,"port":{"path":"/dev/ttyRS485-1","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2}}`, string(s.sent("wb-mqtt-serial", "fw-update", "GetFirmwareInfo")[0].Params))
	// the listed device fed back into a handle: its port block and numeric address
	s.JSONEq(`{"path":"/dev/ttyRS485-1","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2,"protocol":"modbus","slave_id":1,"function":3,"address":0,"count":1,"format":"HEX"}`, string(s.sent("wb-mqtt-serial", "port", "Load")[0].Params))
}

func (s *MqttRpcHelpersSuite) TestDeviceOperations() {
	s.trigger("deviceOps")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [settings: {"baud_rate":96,"in1_mode":1}] (QoS 1)`)
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [read: {"Urms L1":230.1,"Irms L1":0.5}] (QoS 1)`)
	s.logged(`channel: 230.1 channels: {"Urms L1":230.1,"Irms L1":0.5} parameter: 96`)
	s.SkipTill("wbrules-log -> /wbrules/log/info: [paused result: 42 order: inside] (QoS 1)")
	s.logged("paused rethrow: boom")
	loads := s.sent("wb-mqtt-serial", "device", "Load")
	s.JSONEq(`{"device_id":"wb-map12e_1","channels":["Urms L1"]}`, string(loads[1].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","channels":["Urms L1","Irms L1"]}`, string(loads[2].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","parameters":["baud_rate"]}`, string(loads[3].Params))
	sets := s.sent("wb-mqtt-serial", "device", "Set")
	// booleans become 1/0 (the driver rejects JSON booleans as channel values)
	s.JSONEq(`{"device_id":"wb-map12e_1","channels":{"K1":1}}`, string(sets[1].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","channels":{"K1":0,"K2":1},"total_timeout":2000}`, string(sets[2].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","parameters":{"in1_mode":2}}`, string(sets[3].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","parameters":{"in2_mode":3}}`, string(sets[4].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","force":true}`, string(s.sent("wb-mqtt-serial", "device", "LoadConfig")[0].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","channels":["Urms L1"]}`, string(s.sent("wb-mqtt-serial", "device", "Load")[0].Params))
	s.JSONEq(`{"device_id":"wb-map12e_1","parameters":{"baud_rate":96}}`, string(s.sent("wb-mqtt-serial", "device", "Set")[0].Params))
	// pause, resume, pause, resume (the second block threw)
	polls := s.sent("wb-mqtt-serial", "device", "SetPoll")
	s.Require().Len(polls, 4)
	for i, want := range []bool{false, true, false, true} {
		s.JSONEq(`{"device_id":"wb-map12e_1","poll":`+map[bool]string{false: "false", true: "true"}[want]+`}`, string(polls[i].Params), "SetPoll #%d", i)
	}
}

func (s *MqttRpcHelpersSuite) TestHistory() {
	s.trigger("history")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [query: 2 hasMore=true first=wb-adc/Vin 24.3 2025-08-24T01:46:39.250Z] (QoS 1)")
	// two channels -> two compact requests, merged by time
	s.SkipTill("wbrules-log -> /wbrules/log/info: [multi: wb-adc/A1=1.5@1755990000500 wb-adc/Vin=24.3@1755999999250 wb-adc/Vin=24.4@1756000000000] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [channels: wb-adc/Vin:100:2025-08-24T01:46:40.000Z wb-adc/A1:3:2025-08-23T23:00:00.000Z] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [last: 24.4 2025-08-24T01:46:40.000Z] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [last none: undefined] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [avg: 24.25] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [last+since: true] (QoS 1)")
	// the wire: ver 1 with milliseconds, seconds from Date/ms, one channel per request
	s.logged("bad channel: true")
	q := s.params("db_logger", "history", "get_values", 0)
	s.Equal(float64(1), q["ver"])
	s.Equal(true, q["with_milliseconds"])
	s.Equal(float64(10), q["limit"])
	gt := q["timestamp"].(map[string]any)["gt"].(float64)
	s.InDelta(float64(time.Now().Unix())-3600, gt, 5)
	q = s.params("db_logger", "history", "get_values", 1)
	s.Equal(float64(1787529600), q["timestamp"].(map[string]any)["gt"])
	s.Equal(float64(1756080000), q["timestamp"].(map[string]any)["lt"])
	s.Equal([]any{[]any{"wb-adc", "Vin"}}, q["channels"])
	s.Equal([]any{[]any{"wb-adc", "A1"}}, s.params("db_logger", "history", "get_values", 2)["channels"])
	// average: up to 100 server-side buckets, averaged again here
	avg := s.params("db_logger", "history", "get_values", 4)
	s.Equal(float64(100), avg["max_records"])
	s.Nil(avg["limit"])
}

func (s *MqttRpcHelpersSuite) TestEditors() {
	s.trigger("editors")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [rules: a.js] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [rule content: // a] (QoS 1)")
	// the first verdict is pending: the helper sleeps 200 ms (fake timer) and asks again
	s.fireNth(200*time.Millisecond, 1)
	s.SkipTill("wbrules-log -> /wbrules/log/info: [check: ready 1] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [types: 24] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [configs: /etc/x.conf] (QoS 1)")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [updated: {"debug":true,"n":1}] (QoS 1)`)
	s.logged(`replaced: {"replaced":true}`)
	s.JSONEq(`{"path":"a.js","content":"// new"}`, string(s.sent("wbrules", "Editor", "Save")[0].Params))
	states := s.sent("wbrules", "Editor", "ChangeState")
	s.JSONEq(`{"path":"a.js","state":false}`, string(states[0].Params))
	s.JSONEq(`{"path":"a.js","state":true}`, string(states[1].Params))
	s.JSONEq(`{"path":"a.js","new_path":"b.js"}`, string(s.sent("wbrules", "Editor", "Rename")[0].Params))
	s.GreaterOrEqual(len(s.sent("wbrules", "Editor", "Check")), 2, "check polls while pending")
	saves := s.sent("confed", "Editor", "Save")
	s.JSONEq(`{"path":"/etc/x.conf","content":{"debug":true,"n":1}}`, string(saves[0].Params))
	s.JSONEq(`{"path":"/etc/x.conf","content":{"replaced":true}}`, string(saves[1].Params))
}

func (s *MqttRpcHelpersSuite) TestLogs() {
	s.trigger("logs")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [tail: 2025-08-24T01:46:40.123Z 6 second c2 | 2025-08-24T01:46:39.000Z 3 first c1] (QoS 1)")
	s.logged("services: wb-rules.service,wb-mqtt-serial.service boots: b1 2025-08-12T12:00:00.000Z")
	s.JSONEq(`{"service":"wb-rules.service","limit":2}`, string(s.sent("wb_logs", "logs", "Load")[0].Params))
	s.JSONEq(`{"time":1787565600,"levels":[3],"pattern":"x","case-sensitive":false,"cursor":{"id":"c1","direction":"forward"},"limit":5}`, string(s.sent("wb_logs", "logs", "Load")[1].Params))
	// since alone walks forward from that moment (the service's default is backward)
	s.JSONEq(`{"time":1787565600,"cursor":{"direction":"forward"},"limit":3}`, string(s.sent("wb_logs", "logs", "Load")[2].Params))
	s.JSONEq(`{"cursor":{"id":"c2","direction":"backward"},"limit":3}`, string(s.sent("wb_logs", "logs", "Load")[3].Params))
}

func (s *MqttRpcHelpersSuite) TestStateTopics() {
	s.trigger("state")
	// the first update (slave 5) ends its components stage with the wait for a
	// further stage (the second 2 s wait: the first was cut short by the
	// components entry) - let it run out
	s.fireNth(2000*time.Millisecond, 2)
	s.logged("fw stale-first done: fw0,cmp40")
	s.logged("wait recorded error: firmware update failed: recorded error")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [scan found: 11,22 progress: 10,60,100] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [scan state: scanning=false] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [fw update done: 10,60,100,fw0,fw50] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [fw update failed: firmware update failed: device not responding state=10] (QoS 1)")
	s.logged("fw retry done")
	s.logged("diag: diag.zip")
	s.JSONEq(`{"scan_type":"standard","port":{"path":"/dev/ttyRS485-1"}}`, string(s.sent("wb-device-manager", "bus-scan", "Start")[0].Params))
	updates := s.sent("wb-mqtt-serial", "fw-update", "Update")
	s.JSONEq(`{"slave_id":5,"port":{"path":"/dev/ttyRS485-1","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2}}`, string(updates[0].Params))
	s.JSONEq(`{"slave_id":3,"port":{"path":"/dev/ttyRS485-1","baud_rate":9600,"parity":"N","data_bits":8,"stop_bits":2}}`, string(updates[1].Params))
	// wb-device-manager over TCP: ClearError takes the port as "host:port", the info call an address block
	s.JSONEq(`{"slave_id":1,"port":{"path":"10.0.0.5:502"}}`, string(s.sent("wb-device-manager", "fw-update", "ClearError")[0].Params))
	s.JSONEq(`{"slave_id":1,"port":{"address":"10.0.0.5","port":502},"protocol":"modbus-tcp"}`, string(s.sent("wb-device-manager", "fw-update", "GetFirmwareInfo")[0].Params))
}

func (s *MqttRpcHelpersSuite) TestAvailability() {
	s.client.Publish(wbgong.MQTTMessage{Topic: "/rpc/v1/wb-mqtt-serial/config/Load", Payload: "1", QoS: 1, Retained: true})
	s.client.Publish(wbgong.MQTTMessage{Topic: "/rpc/v1/db_logger/history/get_values", Payload: "1", QoS: 1, Retained: true})
	s.publish("/devices/rpchelp/controls/avail/on", "1", "rpchelp/avail")
	// dali has no presence: its 200 ms wait runs out
	s.SkipTill("new fake timer: 2, 200") // timer 1: serial's wait, cancelled by the retained presence
	ts := s.AdvanceTime(200 * time.Millisecond)
	s.FireTimer(2, ts)
	s.SkipTill("wbrules-log -> /wbrules/log/info: [available: serial=true dali=false] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [db available] (QoS 1)")
	s.SkipTill(`wbrules-log -> /wbrules/log/info: [tcp target: {"ip":"10.0.0.5","port":502}] (QoS 1)`)
}

func TestMqttRpcHelpers(t *testing.T) {
	testutils.RunSuites(t, new(MqttRpcHelpersSuite))
}
