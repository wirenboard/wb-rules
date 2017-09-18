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
	DRIVER_CLIENT_ID = "rules"
	DRIVER_CONV_ID   = "wb-rules"
	ENGINE_CLIENT_ID = "wb-rules-engine"

	PERSISTENT_DB_FILE      = "/var/lib/wirenboard/wbrules-persistent.db"
	VIRTUAL_DEVICES_DB_FILE = "/var/lib/wirenboard/wbrules-vdev.db"

	WBRULES_MODULES_ENV = "WB_RULES_MODULES"
)

func main() {
	brokerAddress := flag.String("broker", "tcp://localhost:1883", "MQTT broker url")
	editDir := flag.String("editdir", "", "Editable script directory")
	debug := flag.Bool("debug", false, "Enable debugging")
	noQueues := flag.Bool("debug-queues", false, "Don't use queues in wbgo driver (debugging)")
	useSyslog := flag.Bool("syslog", false, "Use syslog for logging")
	mqttDebug := flag.Bool("mqttdebug", false, "Enable MQTT debugging")
	precise := flag.Bool("precise", false, "Don't reown devices without driver")
	cleanup := flag.Bool("cleanup", false, "Clean up MQTT data on unload")

	persistentDbFile := flag.String("pdb", PERSISTENT_DB_FILE, "Persistent storage DB file")
	vdevDbFile := flag.String("vdb", VIRTUAL_DEVICES_DB_FILE, "Virtual devices values DB file")

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
	wbgo.MaybeInitProfiling(nil)

	driverMqttClient := wbgo.NewPahoMQTTClient(*brokerAddress, DRIVER_CLIENT_ID)
	driverArgs := wbgo.NewDriverArgs().
		SetId(DRIVER_CONV_ID).
		SetMqtt(driverMqttClient).
		SetUseStorage(*vdevDbFile != "").
		SetStoragePath(*vdevDbFile).
		SetReownUnknownDevices(!*precise)

	if *noQueues {
		driverArgs.SetTesting()
	}

	driver, err := wbgo.NewDriverBase(driverArgs)
	if err != nil {
		wbgo.Error.Fatalf("error creating driver: %s", err)
	}

	wbgo.Info.Println("driver is created")

	if err := driver.StartLoop(); err != nil {
		wbgo.Error.Fatalf("error starting the driver: %s", err)
	}

	wbgo.Info.Println("driver loop is started")
	ready := make(chan struct{})
	driver.OnRetainReady(func(tx wbgo.DriverTx) {
		close(ready)
	})
	driver.SetFilter(&wbgo.AllDevicesFilter{})

	wbgo.Info.Println("wait for driver to become ready")
	<-ready
	wbgo.Info.Println("driver is ready")

	engineOptions := wbrules.NewESEngineOptions()
	engineOptions.SetPersistentDBFile(*persistentDbFile)
	engineOptions.SetModulesDirs(strings.Split(os.Getenv(WBRULES_MODULES_ENV), ":"))
	engineOptions.SetCleanupOnStop(*cleanup)

	if *noQueues {
		engineOptions.SetTesting(true)
	}

	engineMqttClient := wbgo.NewPahoMQTTClient(*brokerAddress, ENGINE_CLIENT_ID)
	engine, err := wbrules.NewESEngine(driver, engineMqttClient, engineOptions)
	if err != nil {
		wbgo.Error.Fatalf("error creating engine: %s", err)
	}
	engine.Start()

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
	wbgo.Info.Println("all rule files are loaded")

	if *editDir != "" {
		rpc := wbgo.NewMQTTRPCServer("wbrules", engineMqttClient)
		rpc.Register(wbrules.NewEditor(engine))
		rpc.Start()
	}

	// wait for quit signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c

	engine.Stop()
	driver.StopLoop()
	driver.Close()
}
