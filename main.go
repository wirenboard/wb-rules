package main

import (
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/contactless/wb-rules/wbrules"
	"github.com/contactless/wbgo"
)

const (
	DRIVER_CLIENT_ID      = "rules"
	PERSISTENT_DB_FILE    = "/var/lib/wirenboard/wbrules-persistent.db"
	VIRTUAL_CELLS_DB_FILE = "/var/lib/wirenboard/wbrules-vcells.db"

	MQTT_QUEUE_LEN = 2048

	WBRULES_MODULES_ENV = "WBRULES_MODULES"
)

func main() {
	brokerAddress := flag.String("broker", "tcp://localhost:1883", "MQTT broker url")
	editDir := flag.String("editdir", "", "Editable script directory")
	debug := flag.Bool("debug", false, "Enable debugging")
	useSyslog := flag.Bool("syslog", false, "Use syslog for logging")
	mqttDebug := flag.Bool("mqttdebug", false, "Enable MQTT debugging")
	queueLens := flag.Uint("queue-len", MQTT_QUEUE_LEN, "Set MQTT inner queues lengths")

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
	mqttClient := wbgo.NewPahoMQTTClientQueues(*brokerAddress, DRIVER_CLIENT_ID, true, *queueLens, *queueLens)
	driver := wbgo.NewDriverQueueLens(model, mqttClient, *queueLens, *queueLens)
	driver.SetAutoPoll(false)
	driver.SetAcceptsExternalDevices(true)

	engineOptions := wbrules.NewESEngineOptions()
	engineOptions.SetPersistentDBFile(PERSISTENT_DB_FILE)
	engineOptions.SetVirtualCellsStorageFile(VIRTUAL_CELLS_DB_FILE)
	engineOptions.SetScriptDirs(strings.Split(os.Getenv(WBRULES_MODULES_ENV), ":"))

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

	waitSignals()

	engine.Stop()
}

func waitSignals() {
	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		done <- true
	}()
	<-done
}
