package main

import (
	"flag"
	"os"
	"strings"
	"time"

	"github.com/contactless/wb-rules/wbrules"
	"github.com/contactless/wbgo"
)

const (
	DRIVER_CLIENT_ID      = "rules"
	PERSISTENT_DB_FILE    = "/var/lib/wirenboard/wbrules-persistent.db"
	VIRTUAL_CELLS_DB_FILE = "/var/lib/wirenboard/wbrules-vcells.db"

	WBRULES_MODULES_ENV = "WB_RULES_MODULES"
)

func main() {
	brokerAddress := flag.String("broker", "tcp://localhost:1883", "MQTT broker url")
	editDir := flag.String("editdir", "", "Editable script directory")
	debug := flag.Bool("debug", false, "Enable debugging")
	useSyslog := flag.Bool("syslog", false, "Use syslog for logging")
	mqttDebug := flag.Bool("mqttdebug", false, "Enable MQTT debugging")
	flag.Parse()
	if flag.NArg() < 1 {
		wbgo.Error.Fatal("must specify rule file/directory name(s)")
	}
	if *useSyslog {
		wbgo.UseSyslog()
	}
	if *debug {
		wbgo.SetDebuggingEnabled(true)
	}
	if *mqttDebug {
		wbgo.EnableMQTTDebugLog(*useSyslog)
	}
	model := wbrules.NewCellModel()
	mqttClient := wbgo.NewPahoMQTTClient(*brokerAddress, DRIVER_CLIENT_ID, true)
	driver := wbgo.NewDriver(model, mqttClient)
	driver.SetAutoPoll(false)
	driver.SetAcceptsExternalDevices(true)

	engineOptions := wbrules.NewESEngineOptions()
	engineOptions.SetPersistentDBFile(PERSISTENT_DB_FILE)
	engineOptions.SetVirtualCellsStorageFile(VIRTUAL_CELLS_DB_FILE)
	engineOptions.SetModulesDirs(strings.Split(os.Getenv(WBRULES_MODULES_ENV), ":"))

	engine := wbrules.NewESEngine(model, mqttClient, engineOptions)

	gotSome := false
	watcher := wbgo.NewDirWatcher("\\.js$", engine)
	if *editDir != "" {
		engine.SetSourceRoot(*editDir)
	}
	for _, path := range flag.Args() {
		if err := watcher.Load(path); err != nil {
			wbgo.Error.Printf("error loading script file/dir %s: %s", path, err)
		} else {
			gotSome = true
		}
	}
	if !gotSome {
		wbgo.Error.Fatalf("no valid scripts found")
	}
	if err := driver.Start(); err != nil {
		wbgo.Error.Fatalf("error starting the driver: %s", err)
	}

	if *editDir != "" {
		rpc := wbgo.NewMQTTRPCServer("wbrules", mqttClient)
		rpc.Register(wbrules.NewEditor(engine))
		rpc.Start()
	}

	engine.Start()

	for {
		time.Sleep(1 * time.Second)
	}
}
