# openbu-mock
This is a `Golang` Linux command line program to mock `Bambu` 3D printers, and AMS units. It supports `SSDP` for auto-discovery, and publishes data via `MQTT`. It is intended to be used with [Openbu](https://github.com/cygnusx-1-org/openbu).

## SSDP
This program announces itself via `SSDP`(Simple Service Discovery Protocol) for each mock printer. This lets software auto-discover them. This avoids having to know the ip address and serial number of each mock printer. It also lets other software test their own ability to discover printers. `SSDP` only works on the same `VLAN` without extra steps.

## Randomization
Unless specified by a command line argument things like the `AMS` model, printer model, external spool filament, each tray of the AMS's filament, serial numbers, and access codes are all random. The external spool, and AMS unit trays can also each be `empty`.

# Chamber light
This program will let external programs toggle a mocked chamber light on each mocked printer. Just like it would work with a real printer.

# Camera
This program generates mock camera images that show the model of printer. It also shows the status of the chamber light in real-time.

## Count(number of printers)
The default count is one. If you specify more than one mock printer via `-count` it will use IP aliases on the interface to add more ip addresses, one per mock printer. For the first additional ip address it does this by going up one ip address, testing it with ping, and then deciding to use it. For the second additional address it goes down one, and repeats the same process of testing it with ping, and then deciding to use it. It goes +1, -1, +2, -2, etc.

## sudo
To be able to create ip aliases for additional printers requires `root` privileges under `Linux`. So run `sudo ./mock` if you set the count greater than one.

## Printer models, serial numbers, and device models
This program has tables for the different models of printer to generate, in theory, valid device models and serial numbers.

## Screenshots from Openbu showing mock printers
[Screenshots](https://github.com/cygnusx-1-org/openbu/tree/master/screenshots/mocked)

## Help
```
./mock --help            
Usage of ./mock:
  -access-code string
    	LAN access code (default: random 8 digits)
  -ams string
    	AMS model (AMS-HT, AMS, AMS-2-PRO)
  -count int
    	Number of mock printers to create (default 1)
  -external-spool string
    	External spool filament type (PLA, PETG, ABS, TPU, ASA, PA-CF, PA6-CF, PLA-CF, PETG-CF)
  -model string
    	Printer model (A1-Mini, A1, H2C, H2D, H2D-Pro, H2S, P1P, P1S, P2S, X1, X1C, X1E) (default: random)
```

## Example
This example sets the same `access code` for the three mock printers. It also makes them all `P1S`s. It found ip addresses in the local network for each. It generated random `serial numbers` and `device names`. It also generated different random configurations of AMS units, and the status of trays.

```
sudo ./mock -access-code 11258023 -count 3 -model P1S
Searching for 2 available IPs near 192.168.1.106...
=== Mock Bambu Lab Printers ===

IP               Model     Serial           Code       Device Name    AMS            Ext Spool 
---------------------------------------------------------------------------------------------
192.168.1.106   P1S       01P019E1D239397  11258023   3DP-01P-397    AMS-HT (1/1t)  -         
192.168.1.105   P1S       01P73BB24CB966A  11258023   3DP-01P-66A    -              ABS       
192.168.1.108   P1S       01P43D790BB4E06  11258023   3DP-01P-E06    AMS-2-PRO (3/4t) ABS       

Interface: wlan0 | Count: 3
IP aliases added: 192.168.1.185, 192.168.1.188

Press Ctrl+C to stop.

2026/02/23 03:44:25 SSDP: sent initial NOTIFY for 3 printer(s)
2026/02/23 03:44:25 Camera: listening on 192.168.1.106:6000
2026/02/23 03:44:25 MQTT: listening on 192.168.1.108:8883
2026/02/23 03:44:25 MQTT: listening on 192.168.1.105:8883
2026/02/23 03:44:25 Camera: listening on 192.168.1.108:6000
2026/02/23 03:44:25 MQTT: listening on 192.168.1.106:8883
2026/02/23 03:44:25 Camera: listening on 192.168.1.105:6000
2026/02/23 03:44:38 SSDP: received M-SEARCH from 192.168.1.117:38113
```
