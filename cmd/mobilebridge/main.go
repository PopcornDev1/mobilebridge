// Command mobilebridge runs a CDP-to-Android bridge.
//
// Usage:
//
//	mobilebridge --list
//	mobilebridge --port 9222
//	mobilebridge --device <serial> --port 9222
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/PopcornDev1/mobilebridge/pkg/mobilebridge"
)

func main() {
	var (
		device = flag.String("device", "", "device serial (auto-pick if empty and exactly one is attached)")
		port   = flag.Int("port", 9222, "local TCP port for the CDP server")
		list   = flag.Bool("list", false, "list attached devices and exit")
	)
	flag.Parse()

	if *list {
		if err := runList(); err != nil {
			log.Fatalf("list: %v", err)
		}
		return
	}

	serial, err := resolveSerial(*device)
	if err != nil {
		log.Fatalf("select device: %v", err)
	}
	log.Printf("using device %s", serial)

	proxy, err := mobilebridge.NewProxy(serial, *port)
	if err != nil {
		log.Fatalf("new proxy: %v", err)
	}
	defer proxy.Close()

	srv := mobilebridge.NewServer(serial, fmt.Sprintf("127.0.0.1:%d", *port))
	if err := srv.Start(); err != nil {
		log.Fatalf("start server: %v", err)
	}
	if err := srv.RunWithProxy(proxy); err != nil {
		log.Fatalf("wire proxy: %v", err)
	}
	log.Printf("mobilebridge listening on http://127.0.0.1:%d", *port)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	log.Printf("shutting down")
	_ = srv.Stop()
}

func runList() error {
	devs, err := mobilebridge.ListDevices()
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		fmt.Println("no devices attached")
		return nil
	}
	for _, d := range devs {
		fmt.Printf("%-20s  %-12s  %s %s\n", d.Serial, d.State, d.Model, d.Product)
	}
	return nil
}

func resolveSerial(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	devs, err := mobilebridge.ListDevices()
	if err != nil {
		return "", err
	}
	var ready []mobilebridge.Device
	for _, d := range devs {
		if d.State == "device" {
			ready = append(ready, d)
		}
	}
	switch len(ready) {
	case 0:
		return "", fmt.Errorf("no ready devices found (run `mobilebridge --list`)")
	case 1:
		return ready[0].Serial, nil
	default:
		return "", fmt.Errorf("multiple devices attached; pass --device <serial>")
	}
}
