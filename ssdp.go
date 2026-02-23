package main

import (
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

const (
	ssdpMulticastAddr = "239.255.255.250:2021"
	ssdpPort          = 2021
	notifyInterval    = 5 * time.Second
)

func startSsdp(printers []*Printer) {
	multicastAddr, err := net.ResolveUDPAddr("udp4", ssdpMulticastAddr)
	if err != nil {
		log.Fatalf("SSDP: failed to resolve multicast addr: %v", err)
	}

	// Listen on the SSDP port for M-SEARCH requests
	conn, err := net.ListenMulticastUDP("udp4", nil, multicastAddr)
	if err != nil {
		log.Fatalf("SSDP: failed to listen on multicast: %v", err)
	}
	defer conn.Close()

	// Separate connection for sending (not bound to multicast group)
	sendConn, err := net.DialUDP("udp4", nil, multicastAddr)
	if err != nil {
		log.Fatalf("SSDP: failed to create send connection: %v", err)
	}
	defer sendConn.Close()

	// Send initial NOTIFY for all printers
	for _, p := range printers {
		sendConn.Write([]byte(buildNotify(p)))
	}
	log.Printf("SSDP: sent initial NOTIFY for %d printer(s)", len(printers))

	// Periodic NOTIFY for all printers
	ticker := time.NewTicker(notifyInterval)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			for _, p := range printers {
				sendConn.Write([]byte(buildNotify(p)))
			}
		}
	}()

	// Listen for M-SEARCH and respond with all printers
	buf := make([]byte, 4096)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		msg := string(buf[:n])
		if strings.Contains(msg, "M-SEARCH") {
			log.Printf("SSDP: received M-SEARCH from %s", remoteAddr)
			responseConn, err := net.DialUDP("udp4", nil, remoteAddr)
			if err == nil {
				for _, p := range printers {
					responseConn.Write([]byte(buildNotify(p)))
				}
				responseConn.Close()
			}
		}
	}
}

func buildNotify(p *Printer) string {
	return fmt.Sprintf("NOTIFY * HTTP/1.1\r\n"+
		"HOST: 239.255.255.250:1900\r\n"+
		"Server: UPnP/1.0\r\n"+
		"Location: %s\r\n"+
		"NT: urn:bambulab-com:device:3dprinter:1\r\n"+
		"USN: %s\r\n"+
		"Cache-Control: max-age=1800\r\n"+
		"DevModel.bambu.com: %s\r\n"+
		"DevName.bambu.com: %s\r\n"+
		"DevSignal.bambu.com: -50\r\n"+
		"DevConnect.bambu.com: lan\r\n"+
		"DevBind.bambu.com: free\r\n"+
		"Devseclink.bambu.com: secure\r\n"+
		"DevVersion.bambu.com: 01.09.01.00\r\n"+
		"DevCap.bambu.com: 1\r\n"+
		"\r\n",
		p.IP,
		p.Serial,
		p.DevModel,
		p.DeviceName(),
	)
}
