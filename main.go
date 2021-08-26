package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/wirenboard/wb-rules/wbrules"
	"github.com/wirenboard/wbgong"

	"github.com/alexcesaro/statsd"
)

var version = "unknown"

const (
	DRIVER_CLIENT_ID = "rules"
	DRIVER_CONV_ID   = "wb-rules"
	ENGINE_CLIENT_ID = "wb-rules-engine"

	PERSISTENT_DB_FILE      = "/var/lib/wirenboard/wbrules-persistent.db"
	VIRTUAL_DEVICES_DB_FILE = "/var/lib/wirenboard/wbrules-vdev.db"

	WBRULES_MODULES_ENV = "WB_RULES_MODULES"
)

func main() {

	if os.Args[1] == "version" {
		fmt.Println(version)
		os.Exit(0)
	}

	var err error

	brokerAddress := flag.String("broker", "tcp://localhost:1883", "MQTT broker url")
	editDir := flag.String("editdir", "", "Editable script directory")
	debug := flag.Bool("debug", false, "Enable debugging")
	noQueues := flag.Bool("debug-queues", false, "Don't use queues in wbgo driver (debugging)")
	useSyslog := flag.Bool("syslog", false, "Use syslog for logging")
	mqttDebug := flag.Bool("mqttdebug", false, "Enable MQTT debugging")
	precise := flag.Bool("precise", false, "Don't reown devices without driver")
	cleanup := flag.Bool("cleanup", false, "Clean up MQTT data on unload")

	hostname, err := os.Hostname()
	if err != nil {
		// TODO: maybe generate random string as hostname for this instance
		wbgong.Warn.Print("failed to get hostname for this instance, using 'default'")
		hostname = "default"
	}

	statsdURL := flag.String("statsd", "", "Statsd server address (empty for no statsd communication)")
	statsdPrefix := flag.String("statsd-prefix", hostname, "Statsd prefix for this app instance (hostname by default)")

	persistentDbFile := flag.String("pdb", PERSISTENT_DB_FILE, "Persistent storage DB file")
	vdevDbFile := flag.String("vdb", VIRTUAL_DEVICES_DB_FILE, "Virtual devices values DB file")

	wbgoso := flag.String("wbgo", "/usr/share/wb-rules/wbgo.so", "Location to wbgo.so file")

	flag.Parse()

	if flag.NArg() < 1 {
		wbgong.Error.Fatal("must specify rule file/directory name(s)")
	}
	if *useSyslog {
		wbgong.UseSyslog()
	}
	if *debug {
		wbgong.SetDebuggingEnabled(true)
	}

	errInit := wbgong.Init(*wbgoso)
	if errInit != nil {
		log.Fatalf("ERROR in init wbgo.so: '%s'", errInit)
	}

	if *mqttDebug {
		wbgong.EnableMQTTDebugLog(*useSyslog)
	}
	wbgong.MaybeInitProfiling(nil)

	// prepare statsd client if required
	var statsdClient wbgong.StatsdClientWrapper
	var runtimeStatsd wbgong.StatsdRuntimeCollector
	if *statsdURL != "" {
		if statsdClient, err = wbgong.NewStatsdClientWrapper("wb-rules", statsd.Address(*statsdURL), statsd.Prefix(*statsdPrefix)); err != nil {
			wbgong.Error.Fatalf("failed to create statsd client: %s", err)
		}
		runtimeStatsd = wbgong.NewStatsdRuntimeCollector(statsdClient)
		runtimeStatsd.Start()
		defer runtimeStatsd.Stop()
	}

	// prepare exit signal channel
	exitCh := make(chan os.Signal, 1)
	signal.Notify(exitCh, syscall.SIGINT, syscall.SIGTERM)

	driverMqttClient := wbgong.NewPahoMQTTClient(*brokerAddress, DRIVER_CLIENT_ID)
	driverArgs := wbgong.NewDriverArgs().
		SetId(DRIVER_CONV_ID).
		SetMqtt(driverMqttClient).
		SetUseStorage(*vdevDbFile != "").
		SetStoragePath(*vdevDbFile).
		SetReownUnknownDevices(!*precise).
		SetStatsdClient(statsdClient)

	if *noQueues {
		driverArgs.SetTesting()
	}

	driver, err := wbgong.NewDriverBase(driverArgs)
	if err != nil {
		wbgong.Error.Fatalf("error creating driver: %s", err)
	}

	wbgong.Info.Println("driver is created")

	if err := driver.StartLoop(); err != nil {
		wbgong.Error.Fatalf("error starting the driver: %s", err)
	}
	driver.WaitForReady()

	wbgong.Info.Println("driver loop is started")
	driver.SetFilter(&wbgong.AllDevicesFilter{})

	wbgong.Info.Println("wait for driver to become ready")
	driver.WaitForReady()
	wbgong.Info.Println("driver is ready")

	engineOptions := wbrules.NewESEngineOptions()
	engineOptions.SetPersistentDBFile(*persistentDbFile)
	engineOptions.SetModulesDirs(strings.Split(os.Getenv(WBRULES_MODULES_ENV), ":"))
	engineOptions.SetCleanupOnStop(*cleanup)
	engineOptions.SetStatsdClient(statsdClient)

	if *noQueues {
		engineOptions.SetTesting(true)
	}

	engineMqttClient := wbgong.NewPahoMQTTClient(*brokerAddress, ENGINE_CLIENT_ID)
	engine, err := wbrules.NewESEngine(driver, engineMqttClient, engineOptions)
	if err != nil {
		wbgong.Error.Fatalf("error creating engine: %s", err)
	}
	engine.Start()

	gotSome := false
	watcher := wbgong.NewDirWatcher("\\.js(\\"+wbrules.FILE_DISABLED_SUFFIX+")?$", engine)
	if *editDir != "" {
		engine.SetSourceRoot(*editDir)
	}
	for _, path := range flag.Args() {
		if err := watcher.Load(path); err != nil {
			wbgong.Error.Printf("error loading script file/dir %s: %s", path, err)
		} else {
			gotSome = true
		}
	}
	if !gotSome {
		wbgong.Error.Fatalf("no valid scripts found")
	}
	wbgong.Info.Println("all rule files are loaded")

	if *editDir != "" {
		rpc := wbgong.NewMQTTRPCServer("wbrules", engineMqttClient)
		rpc.Register(wbrules.NewEditor(engine))
		rpc.Start()
	}

	// wait for quit signal
	<-exitCh

	engine.Stop()
	driver.StopLoop()
	driver.Close()
}
