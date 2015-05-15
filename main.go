package main

import (
	wbrules "./wbrules"
	"flag"
	"github.com/contactless/wbgo"
	"time"
)

const DRIVER_CLIENT_ID = "rules"

func main() {
	brokerAddress := flag.String("broker", "tcp://localhost:1883", "MQTT broker url")
	debug := flag.Bool("debug", false, "Enable debugging")
	useSyslog := flag.Bool("syslog", false, "Use syslog for logging")
	flag.Parse()
	if flag.NArg() < 1 {
		wbgo.Error.Fatal("must specify rule file name(s)")
	}
	if *useSyslog {
		wbgo.UseSyslog()
	}
	if *debug {
		wbgo.SetDebuggingEnabled(true)
	}
	model := wbrules.NewCellModel()
	mqttClient := wbgo.NewPahoMQTTClient(*brokerAddress, DRIVER_CLIENT_ID, true)
	driver := wbgo.NewDriver(model, mqttClient)
	driver.SetAutoPoll(false)
	driver.SetAcceptsExternalDevices(true)
	engine := wbrules.NewRuleEngine(model, mqttClient)
	gotSome := false
	loader := wbrules.NewLoader("\\.js$", func(filePath string, reloaded bool) (err error) {
		if reloaded {
			err = engine.LiveLoadScript(filePath)
		} else {
			err = engine.LoadScript(filePath)
		}
		return
	})
	for _, path := range flag.Args() {
		if err := loader.Load(path); err != nil {
			wbgo.Error.Printf("error loading script file %s: %s", path, err)
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
	engine.Start()
	for {
		time.Sleep(1 * time.Second)
	}
}
