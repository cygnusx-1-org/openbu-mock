package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	mrand "math/rand"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

var serialPrefixes = map[string]string{
	"A1-MINI": "030",
	"A1":      "039",
	"H2C":     "31B",
	"H2D":     "094",
	"H2D-PRO": "239",
	"H2S":     "093",
	"P1P":     "01S",
	"P1S":     "01P",
	"P2S":     "22E",
	"X1":      "00M",
	"X1C":     "00M",
	"X1E":     "03W",
}

var devModelCodes = map[string]string{
	"A1-MINI": "N1",
	"A1":      "N2S",
	"H2C":     "O1C2",
	"H2D":     "O1D",
	"H2D-PRO": "O1E",
	"H2S":     "O1S",
	"P1P":     "C11",
	"P1S":     "C12",
	"P2S":     "N7",
	"X1":      "BL-P002",
	"X1C":     "BL-P001",
	"X1E":     "C13",
}

var amsTrayCounts = map[string]int{
	"AMS-HT":    1,
	"AMS":       4,
	"AMS-2-PRO": 4,
}

func main() {
	model := flag.String("model", "", "Printer model (A1-Mini, A1, H2C, H2D, H2D-Pro, H2S, P1P, P1S, P2S, X1, X1C, X1E) (default: random)")
	amsModel := flag.String("ams", "", "AMS model (AMS-HT, AMS, AMS-2-PRO)")
	extSpool := flag.String("external-spool", "", "External spool filament type (PLA, PETG, ABS, TPU, ASA, PA-CF, PA6-CF, PLA-CF, PETG-CF)")
	accessCode := flag.String("access-code", "", "LAN access code (default: random 8 digits)")
	count := flag.Int("count", 1, "Number of mock printers to create")
	flag.Parse()

	*model = strings.ToUpper(*model)
	*amsModel = strings.ToUpper(*amsModel)
	*extSpool = strings.ToUpper(*extSpool)
	*accessCode = strings.ToUpper(*accessCode)
	randomizeModel := *model == ""

	if !randomizeModel {
		if _, ok := serialPrefixes[*model]; !ok {
			fmt.Fprintf(os.Stderr, "Unknown model: %s\nValid models: A1-MINI, A1, H2C, H2D, H2D-PRO, H2S, P1P, P1S, P2S, X1, X1C, X1E\n", *model)
			os.Exit(1)
		}
	}

	if *amsModel != "" {
		if _, ok := amsTrayCounts[*amsModel]; !ok {
			fmt.Fprintf(os.Stderr, "Unknown AMS model: %s\nValid models: AMS-HT, AMS, AMS-2-PRO\n", *amsModel)
			os.Exit(1)
		}
	}

	if *extSpool != "" {
		if _, ok := filamentInfo[*extSpool]; !ok {
			fmt.Fprintf(os.Stderr, "Unknown filament type: %s\nValid types: PLA, PETG, ABS, TPU, ASA, PA-CF, PA6-CF, PLA-CF, PETG-CF\n", *extSpool)
			os.Exit(1)
		}
	}
	randomizeExtSpool := *extSpool == ""

	if *count < 1 {
		fmt.Fprintf(os.Stderr, "Count must be at least 1\n")
		os.Exit(1)
	}

	// Detect base IP and interface
	baseIP, iface, err := getLocalIPAndInterface()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to detect network: %v\n", err)
		os.Exit(1)
	}

	// Find available IPs for additional printers (zigzag: +1, -1, +2, -2, ...)
	var aliasIPs []string
	if *count > 1 {
		fmt.Printf("Searching for %d available IPs near %s...\n", *count-1, baseIP)
		var err error
		aliasIPs, err = findAvailableIPs(baseIP, *count-1)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to find available IPs: %v\n", err)
			os.Exit(1)
		}
	}

	// Add IP aliases
	var addedAliases []string
	for _, ip := range aliasIPs {
		if err := addIPAlias(iface, ip); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to add IP alias: %v\n", err)
			cleanupAliases(iface, addedAliases)
			os.Exit(1)
		}
		addedAliases = append(addedAliases, ip)
	}

	// Build list of all IPs: base IP first, then aliases
	allIPs := append([]string{baseIP}, aliasIPs...)

	// Build list of model names for random selection
	modelNames := make([]string, 0, len(serialPrefixes))
	for m := range serialPrefixes {
		modelNames = append(modelNames, m)
	}

	// Create printers
	var printers []*Printer
	for i := 0; i < *count; i++ {
		printerModel := *model
		if randomizeModel {
			printerModel = modelNames[mrand.Intn(len(modelNames))]
		}

		prefix := serialPrefixes[printerModel]
		serial := prefix + randomHex(12)

		code := *accessCode
		if code == "" {
			code = randomAlphaNum(8)
		}

		// Each printer gets a random AMS if not specified
		ams := *amsModel
		if ams == "" {
			choices := []string{"", "AMS-HT", "AMS", "AMS-2-PRO"}
			ams = choices[mrand.Intn(len(choices))]
		}

		ext := *extSpool
		if randomizeExtSpool {
			ext = "RANDOM"
		}

		printer := NewPrinter(serial, printerModel, allIPs[i], code, ams, ext)
		printers = append(printers, printer)
	}

	// Print summary
	fmt.Println("=== Mock Bambu Lab Printers ===")
	fmt.Println()
	fmt.Printf("%-16s %-9s %-16s %-10s %-14s %-14s %-10s\n", "IP", "Model", "Serial", "Code", "Device Name", "AMS", "Ext Spool")
	fmt.Println(strings.Repeat("-", 93))
	for _, p := range printers {
		amsInfo := "-"
		if p.ams != nil {
			loaded := 0
			for _, t := range p.ams.Trays {
				if t.TrayType != "" {
					loaded++
				}
			}
			amsInfo = fmt.Sprintf("%s (%d/%dt)", p.ams.Model, loaded, len(p.ams.Trays))
		}
		extInfo := "-"
		if p.vtTray != nil {
			extInfo = p.vtTray.TrayType
		}
		fmt.Printf("%-16s %-9s %-16s %-10s %-14s %-14s %-10s\n", p.IP, p.Model, p.Serial, p.AccessCode, p.DeviceName(), amsInfo, extInfo)
	}
	fmt.Println()
	fmt.Printf("Interface: %s | Count: %d\n", iface, *count)
	if len(addedAliases) > 0 {
		fmt.Printf("IP aliases added: %s\n", strings.Join(addedAliases, ", "))
	}
	fmt.Println()
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	// Start services
	go startSsdp(printers)
	for _, p := range printers {
		go startMqtt(p)
		go startCamera(p)
	}

	// Wait for signal, then cleanup
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("\nShutting down.")
	cleanupAliases(iface, addedAliases)
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	hex := fmt.Sprintf("%X", b)
	return hex[:n]
}

func randomAlphaNum(n int) string {
	const chars = "0123456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		addrs, _ := net.InterfaceAddrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
