package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	mrand "math/rand"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

const Version = "1.0"

var debug *bool

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
	amsFlag := flag.String("ams", "", "AMS config: NONE or MODEL[:COUNT][,MODEL[:COUNT],...] (e.g. NONE, AMS:4, AMS-HT:2,AMS-2-PRO:3)")
	extSpool := flag.String("external-spool", "", "External spool filament type (PLA, PETG, ABS, TPU, ASA, PA-CF, PA6-CF, PLA-CF, PETG-CF)")
	accessCode := flag.String("access-code", "", "LAN access code (default: random 8 digits)")
	count := flag.Int("count", 1, "Number of mock printers to create")
	hmsPreset := flag.Int("hms", 0, "HMS error preset number to inject (0 = random, model-specific; P2S: 1-3, X1/X1C/X1E: 1-4)")
	debug = flag.Bool("debug", false, "Print MQTT status JSON as it is published")
	showVersion := flag.Bool("version", false, "Print program version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("openbu-mock %s\n", Version)
		os.Exit(0)
	}

	*model = strings.ToUpper(*model)
	*amsFlag = strings.ToUpper(*amsFlag)
	*extSpool = strings.ToUpper(*extSpool)
	*accessCode = strings.ToUpper(*accessCode)
	randomizeModel := *model == ""

	if !randomizeModel {
		if _, ok := serialPrefixes[*model]; !ok {
			fmt.Fprintf(os.Stderr, "Unknown model: %s\nValid models: A1-MINI, A1, H2C, H2D, H2D-PRO, H2S, P1P, P1S, P2S, X1, X1C, X1E\n", *model)
			os.Exit(1)
		}
	}

	// Parse AMS specs
	var amsSpecs []AmsSpec
	randomizeAMS := *amsFlag == ""
	if *amsFlag != "" && *amsFlag != "NONE" {
		parts := strings.Split(*amsFlag, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			amsModel := part
			amsCount := 1
			if idx := strings.LastIndex(part, ":"); idx != -1 {
				amsModel = part[:idx]
				n, err := strconv.Atoi(part[idx+1:])
				if err != nil || n < 1 {
					fmt.Fprintf(os.Stderr, "Invalid AMS count in %q\n", part)
					os.Exit(1)
				}
				amsCount = n
			}
			if _, ok := amsTrayCounts[amsModel]; !ok {
				fmt.Fprintf(os.Stderr, "Unknown AMS model: %s\nValid models: NONE, AMS-HT, AMS, AMS-2-PRO\n", amsModel)
				os.Exit(1)
			}
			amsSpecs = append(amsSpecs, AmsSpec{Model: amsModel, Count: amsCount})
		}

		// Validate total AMS count
		totalAMS := 0
		for _, s := range amsSpecs {
			totalAMS += s.Count
		}
		maxAMS := 4
		if !randomizeModel {
			if *model == "H2D" || *model == "H2D-PRO" {
				maxAMS = 12
			}
		}
		if totalAMS > maxAMS {
			fmt.Fprintf(os.Stderr, "Too many AMS units: %d (max %d for this printer)\n", totalAMS, maxAMS)
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

	if *hmsPreset < 0 {
		fmt.Fprintf(os.Stderr, "Invalid --hms preset: %d (must be >= 0; 0 = random)\n", *hmsPreset)
		os.Exit(1)
	}
	if *hmsPreset > 0 && !randomizeModel {
		key := hmsModelKey(*model)
		presets, hasPresets := HMSPresets[key]
		if !hasPresets {
			fmt.Fprintf(os.Stderr, "--hms is not supported for model %s (no HMS presets defined)\n", *model)
			os.Exit(1)
		}
		if *hmsPreset > len(presets) {
			fmt.Fprintf(os.Stderr, "Invalid --hms preset %d for model %s: valid range is 1-%d\n", *hmsPreset, *model, len(presets))
			os.Exit(1)
		}
	}

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

		// Each printer gets random AMS if not specified
		specs := amsSpecs
		if randomizeAMS {
			amsModels := []string{"AMS-HT", "AMS", "AMS-2-PRO"}
			// 25% chance of no AMS, otherwise 1-4 random units
			if mrand.Intn(4) == 0 {
				specs = nil
			} else {
				n := mrand.Intn(4) + 1
				amsModel := amsModels[mrand.Intn(len(amsModels))]
				specs = []AmsSpec{{Model: amsModel, Count: n}}
			}
		}

		ext := *extSpool
		if randomizeExtSpool {
			ext = "RANDOM"
		}

		printer := NewPrinter(serial, printerModel, allIPs[i], code, specs, ext, *hmsPreset)
		printers = append(printers, printer)
	}

	// Print summary
	fmt.Printf("=== Mock Bambu Lab Printers (openbu-mock %s) ===\n", Version)
	fmt.Println()
	fmt.Printf("%-16s %-9s %-16s %-10s %-14s %-14s %-10s\n", "IP", "Model", "Serial", "Code", "Device Name", "AMS", "Ext Spool")
	fmt.Println(strings.Repeat("-", 93))
	for _, p := range printers {
		amsInfo := "-"
		if len(p.ams) > 0 {
			// Count units by model
			counts := map[string]int{}
			totalLoaded := 0
			totalTrays := 0
			for _, a := range p.ams {
				counts[a.Model]++
				totalTrays += len(a.Trays)
				for _, t := range a.Trays {
					if t.TrayType != "" {
						totalLoaded++
					}
				}
			}
			var parts []string
			for m, c := range counts {
				parts = append(parts, fmt.Sprintf("%dx%s", c, m))
			}
			amsInfo = fmt.Sprintf("%s (%d/%dt)", strings.Join(parts, "+"), totalLoaded, totalTrays)
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

	// Load or generate shared CA
	ca, created := LoadOrGenerateCA("ca.pem", "ca-key.pem")
	if created {
		fmt.Println("Generated new CA certificate: ca.pem / ca-key.pem")
	} else {
		fmt.Println("Loaded existing CA certificate: ca.pem / ca-key.pem")
	}
	fmt.Println()

	// Start services
	go startSsdp(printers)
	for _, p := range printers {
		go startMqtt(ca, p)
		if p.Model == "P1P" || p.Model == "P1S" || p.Model == "A1" || p.Model == "A1-MINI" {
			go startCamera(ca, p)
		}
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
