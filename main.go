package main

import (
	"log"
	"flag"
	"time"
	wbrules "./wbrules"
	"github.com/contactless/wbgo"
)

const DRIVER_CLIENT_ID = "rules"

func main () {
	brokerAddress := flag.String("broker", "tcp://localhost:1883", "MQTT broker url")
	flag.Parse()
	log.Printf("args: %v", flag.Args())
	if flag.NArg() < 1 {
		log.Fatal("must specify rule file name(s)")
	}
	model := wbrules.NewCellModel()
	mqttClient := wbgo.NewPahoMQTTClient(*brokerAddress, DRIVER_CLIENT_ID)
	driver := wbgo.NewDriver(model, mqttClient)
	driver.SetAutoPoll(false)
	driver.SetAcceptsExternalDevices(true)
	engine := wbrules.NewRuleEngine(model, mqttClient)
	for _, path := range flag.Args() {
		if err := engine.LoadScript(path); err != nil {
			log.Fatalf("error loading script file %s: %s", path, err);
		}
	}
	if err := driver.Start(); err != nil {
		log.Fatalf("error starting the driver: %s", err)
	}
	engine.Start()
	for {
		time.Sleep(1 * time.Second)
	}
}
