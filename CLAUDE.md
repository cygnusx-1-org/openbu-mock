# Mock Bambu Lab Printer

## Input (Requirements)

1. SSDP auto-discovery on port 2021.
2. Use the IP address of the computer the mock process is running on as the Location.
3. Fill in the last 12 digits of the USN/serial number with a random capitalized hex value.
4. Make the last 3 digits of the DevName the last 3 of the USN/serial number.
5. Allow argument options to be entered as upper or lower case, and then internally capitalize them.
6. Default model is P1S, but create a command line argument for model that uses the matching serial number prefix.
7. AMS support with command line argument to specify model. Use the second value as the number of trays to mock. Randomize the filament color (hex value).

### AMS Models

| Model      | Trays |
|------------|-------|
| AMS-HT     | 1     |
| AMS        | 4     |
| AMS-2-PRO  | 4     |

### Serial Number Prefixes

| Printer | Prefix |
|---------|--------|
| A1-MINI| 030 |
| A1 | 039 |
| H2C | 31B |
| H2D | 094 |
| H2D-PRO | 239 |
| H2S | 093 |
| P1P | 01S |
| P1S | 01P |
| P2S | 22E |
| X1 | 00M |
| X1C | 00M |
| X1E | 03W |

### Dev Model Codes
| Printer | Code |
|------------|-------|
| A1-MINI | N1 |
| A1 | N2S |
| H2C | O1C2 |
| H2D | O1D |
| H2D-PRO | O1E |
| H2S | O1S |
| P1P | C11 |
| P1S | C12 |
| P2S | N7 |
| X1 | BL-P002 |
| X1C | BL-P001 |
| X1E | C13 |

### SSDP Format

```
NOTIFY * HTTP/1.1
HOST: 239.255.255.250:1900
Server: UPnP/1.0
Location: <ip-address>
NT: urn:bambulab-com:device:3dprinter:1
USN: <serial>
Cache-Control: max-age=1800
DevModel.bambu.com: <model-code>
DevName.bambu.com: 3DP-<prefix>-<last-3-of-serial>
DevSignal.bambu.com: -50
DevConnect.bambu.com: lan
DevBind.bambu.com: free
Devseclink.bambu.com: secure
DevVersion.bambu.com: 01.09.01.00
DevCap.bambu.com: 1
```

### MQTT

- TLS on port 8883 with self-signed certificate
- Auth: username `bblp`, password = access code
- Subscribe topic: `device/{serial}/report`
- Publish topic: `device/{serial}/request`
- Full printer status JSON pushed periodically and on `pushall` request
- Handles `ledctrl` commands for chamber light toggle

## Output (Plan)

### Architecture

```
mock/
├── CLAUDE.md      # This file
├── go.mod         # Go module
├── main.go        # CLI argument parsing, signal handling, orchestration
├── ssdp.go        # SSDP multicast NOTIFY broadcaster + M-SEARCH responder
├── mqtt.go        # Raw MQTT 3.1.1 server over TLS (self-signed cert)
└── state.go       # Printer state (temps, fans, AMS, lights, status JSON)
```

### Implementation Steps

1. **`main.go`** — Parse CLI flags (`--model`, `--ams`, `--access-code`), capitalize inputs, validate model/AMS against known maps, generate serial number (prefix + 12 random hex), detect local IP, print config summary, start SSDP and MQTT goroutines, wait for SIGINT/SIGTERM.

2. **`state.go`** — `Printer` struct holding serial, model, IP, access code, light state, AMS config. Methods to generate the full MQTT status JSON matching the Bambu format (including `print.ams.ams[]` with trays, `lights_report`, temperatures, fan speeds, gcode_state). Thread-safe with `sync.RWMutex`. Random filament colors generated at startup.

3. **`ssdp.go`** — Join multicast group `239.255.255.250` on port 2021. Send periodic NOTIFY (every 30s) to multicast. Listen for M-SEARCH requests and respond via unicast to sender. NOTIFY format matches Bambu's SSDP output exactly.

4. **`mqtt.go`** — Generate self-signed TLS certificate in memory. Listen on port 8883. For each connection: parse CONNECT packet (validate username=`bblp`, password=access code), send CONNACK, parse SUBSCRIBE (validate topic matches `device/{serial}/report`), send SUBACK, then enter read/write loop. Push full status JSON every 5 seconds on the report topic. Handle incoming PUBLISH on request topic (parse `pushall` and `ledctrl` commands). Handle PINGREQ with PINGRESP.

### CLI Usage

```
go run ./mock [flags]

Flags:
  --model <MODEL>        Printer model: X1C, X1E, P1S, P1P (default: P1S)
  --ams <AMS_MODEL>      AMS model: AMS-HT, AMS, AMS-2-PRO (optional)
  --access-code <CODE>   LAN access code (default: random 8 digits)
```

All flag values are case-insensitive (internally uppercased).
