package main

import (
	"flag"
	"time"
	wbrules "./wbrules"
	"github.com/contactless/wbgo"
)

const DRIVER_CLIENT_ID = "rules"

func main () {
	brokerAddress := flag.String("broker", "tcp://localhost:1883", "MQTT broker url")
	debug := flag.Bool("debug", false, "Enable debugging")
	flag.Parse()
	if flag.NArg() < 1 {
		wbgo.Error.Fatal("must specify rule file name(s)")
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
	for _, path := range flag.Args() {
		if err := engine.LoadScript(path); err != nil {
			wbgo.Error.Printf("error loading script file %s: %s", path, err);
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
