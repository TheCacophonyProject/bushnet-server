package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	cptvFolder       = "/var/spool/cptv/downloaded"
	avahiServiceType = "_cacophonator-management._tcp"
	ledTriggerFile   = "/sys/class/leds/led0/trigger"
)

type device struct {
	Name string
	IPv4 string
	Port int
}

func (d device) getRecordingsList() []string {
	resp, err := http.Get(d.getAddr() + "/api/recordings")
	if err != nil {
		log.Println(err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Println("non 200 response when getting recordings list")
		return nil
	}
	var ids []string
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&ids); err != nil {
		log.Println(err)
		return nil
	}
	return ids
}

func (d device) getRecording(cptvFolder, id string) error {
	setLedState("blinking")
	resp, err := http.Get(d.getAddr() + "/api/recording/" + id)
	if err != nil {
		log.Println(err)
		return nil
	}
	defer resp.Body.Close()

	out, err := os.Create(path.Join(cptvFolder, d.Name+"_"+id))
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	return d.deleteRecording(id)
}

func (d device) deleteRecording(id string) error {
	req, err := http.NewRequest("DELETE", d.getAddr()+"/api/recording/"+id, nil)
	client := new(http.Client)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New("non 200 status code")
	}
	return nil
}

func (d device) getRecordings(cptvFolder string) {
	log.Printf("searching for recordings on '%s'", d.Name)
	for _, id := range d.getRecordingsList() {
		log.Printf("getting recording '%s'", id)
		err := d.getRecording(cptvFolder, id)
		if err != nil {
			log.Println(err)
		}
	}
}
func (d device) getAddr() string {
	return fmt.Sprintf("http://%s:%d", d.IPv4, d.Port)
}

var ledStates = map[string]string{
	"blinking": "timer",
	"off":      "none",
	"on":       "default-on",
}

func main() {
	err := runMain()
	if err != nil {
		log.Fatal(err.Error())
	}
}

func runMain() error {
	log.SetFlags(0) // Removes default timestamp SetFlags
	os.MkdirAll(cptvFolder, 0755)
	setLedState("off")
	defer setLedState("off")
	for {
		devices := getDevices()
		for _, device := range devices {
			device.getRecordings(cptvFolder)
		}
		if len(devices) > 0 {
			setLedState("on")
		} else {
			setLedState("off")
		}
	}
}

func setLedState(s string) {
	newState := ledStates[s]
	if newState == "" {
		log.Printf("unknown LED state '%s'", s)
		return
	}

	b, err := ioutil.ReadFile(ledTriggerFile)
	if err != nil {
		return // Failed to read LED trigger file, probably because this is not being run on a raspberry pi
	}
	// This is to prevent writing the state to 'blinking' too often as this can make the LED not look like it is blinking.
	if strings.Contains(string(b), "["+newState+"]") {
		return
	}

	cmd := fmt.Sprintf("echo %s > %s", newState, ledTriggerFile)
	if _, err := exec.Command("sh", "-c", cmd).Output(); err != nil {
		log.Println(err)
	}
}

func getDevices() []device {
	devices := []device{}
	log.Println("starting search for devices...")
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		log.Fatalln("Failed to initialize resolver:", err.Error())
		return nil
	}

	entries := make(chan *zeroconf.ServiceEntry)
	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			r := device{
				Name: entry.HostName[:len(entry.HostName)-7],
				IPv4: entry.AddrIPv4[0].String(),
				Port: entry.Port,
			}
			devices = append(devices, r)
		}
	}(entries)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	err = resolver.Browse(ctx, avahiServiceType, "local.", entries)
	if err != nil {
		log.Fatalln("Failed to browse:", err.Error())
	}

	<-ctx.Done()
	log.Printf("found %d devices", len(devices))
	return devices
}
