package main

import (
	wbrules "./wbrules"
	"flag"
	"fmt"
	"github.com/contactless/wbgo"
	"os"
	"os/signal"
	"syscall"
	"time"
	// "runtime/debug"
	MQTT "github.com/contactless/org.eclipse.paho.mqtt.golang"
	"runtime/pprof"
)

const DRIVER_CLIENT_ID = "rules"

func getRusage() (userTime time.Duration, sysTime time.Duration) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		fmt.Fprintln(os.Stderr, "*** -cpuprofile: Getrusage() failed")
		return 0, 0
	}
	userTime = time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond
	sysTime = time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
	return
}

// TBD: move to wbgo (together with flags)
// also, make it possible to disable gc there using flags
func profile(profFile string, dieAfter time.Duration, readyCh <-chan struct{}) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		if readyCh != nil {
			<-readyCh
		}
		start := time.Now()
		initialUserTime, initialSysTime := getRusage()
		packetsInitiallySent, packetsInitiallyReceived := MQTT.GetStats()

		f, err := os.Create(profFile)
		if err != nil {
			wbgo.Error.Fatalf("error creating profiling file: %s", err)
		}
		pprof.StartCPUProfile(f)

		// TBD: add timer
		var dieCh <-chan time.Time = nil
		if dieAfter > 0 {
			dieCh = time.After(dieAfter)
		}
		select {
		case <-ch:
		case <-dieCh:
		}
		newUserTime, newSysTime := getRusage()
		elapsed := time.Since(start)
		if elapsed == 0 {
			fmt.Fprintln(os.Stderr, "oops, no time elapsed?")
			return
		}
		elapsedUserTime := newUserTime - initialUserTime
		elapsedSysTime := newSysTime - initialSysTime
		elapsedCpuTime := elapsedUserTime + elapsedSysTime
		cpuLoadUser := float64(elapsedUserTime) * 100.0 / float64(elapsed)
		cpuLoadSys := float64(elapsedSysTime) * 100.0 / float64(elapsed)
		cpuLoad := float64(elapsedCpuTime) * 100.0 / float64(elapsed)

		newPacketsSent, newPacketsReceived := MQTT.GetStats()
		packetsSent := newPacketsSent - packetsInitiallySent
		packetsReceived := newPacketsReceived - packetsInitiallyReceived
		if packetsSent > 0 || packetsReceived > 0 {
			packetsSentPerSecond := float64(packetsSent) * float64(time.Second) /
				float64(elapsed)
			packetsReceivedPerSecond := float64(packetsReceived) * float64(time.Second) /
				float64(elapsed)
			packetsSentPerCpuSecond := float64(packetsSent) * float64(time.Second) /
				float64(elapsedCpuTime)
			packetsReceivedPerCpuSecond := float64(packetsReceived) * float64(time.Second) /
				float64(elapsedCpuTime)

			fmt.Printf("\n*** %d packets sent (%.2f per sec, %.2f per cpu sec), "+
				"%d packets received (%.2f per sec, %.2f per cpu sec)\n",
				packetsSent, packetsSentPerSecond, packetsSentPerCpuSecond,
				packetsReceived, packetsReceivedPerSecond, packetsReceivedPerCpuSecond)
		}

		fmt.Printf("*** %.2f seconds elapsed, %.2f user, %.2f sys\n",
			float64(elapsed)/float64(time.Second),
			float64(elapsedUserTime)/float64(time.Second),
			float64(elapsedSysTime)/float64(time.Second))
		fmt.Printf("*** %.2f%% CPU load, %.2f%% user, %.2f%% sys\n",
			cpuLoad, cpuLoadUser, cpuLoadSys)
		pprof.StopCPUProfile()
		os.Exit(130)
	}()
}

func main() {
	brokerAddress := flag.String("broker", "tcp://localhost:1883", "MQTT broker url")
	editDir := flag.String("editdir", "", "Editable script directory")
	debug := flag.Bool("debug", false, "Enable debugging")
	useSyslog := flag.Bool("syslog", false, "Use syslog for logging")
	mqttDebug := flag.Bool("mqttdebug", false, "Enable MQTT debugging")
	dieAfter := flag.Int("die", 0, "Die after specified number of seconds")
	cpuprofile := flag.String("cpuprofile", "", "write cpu profile to file")
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
		wbgo.EnableMQTTDebugLog()
	}
	model := wbrules.NewCellModel()
	mqttClient := wbgo.NewPahoMQTTClient(*brokerAddress, DRIVER_CLIENT_ID, true)
	driver := wbgo.NewDriver(model, mqttClient)
	driver.SetAutoPoll(false)
	driver.SetAcceptsExternalDevices(true)
	engine := wbrules.NewESEngine(model, mqttClient)
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

	if *cpuprofile != "" {
		profile(*cpuprofile, time.Duration(*dieAfter)*time.Second, engine.ReadyCh())
	}

	for {
		time.Sleep(1 * time.Second)
	}
}
