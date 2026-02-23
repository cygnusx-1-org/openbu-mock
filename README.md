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

## Examples
### Command line output
This example sets the same `access code` for the three mock printers. It also makes them all `P1S`s. It found ip addresses in the local network for each. It generated random `serial numbers` and `device names`. It also generated different random configurations of AMS units, and the status of trays.

```
sudo ./mock -access-code 11258023 -count 3 -model P1S
earching for 2 available IPs near 192.168.1.106...
=== Mock Bambu Lab Printers ===

IP               Model     Serial           Code       Device Name    AMS            Ext Spool
---------------------------------------------------------------------------------------------
192.168.1.106   P1S       01P4F8E4EEA2CF1  11258023   3DP-01P-CF1    AMS-2-PRO (4/4t) PLA
192.168.1.105   P1S       01P6CB939FA2E27  11258023   3DP-01P-E27    AMS (3/4t)     PA-CF
192.168.1.108   P1S       01P32E8DC345075  11258023   3DP-01P-075    AMS-2-PRO (1/4t) -

Interface: wlan0 | Count: 3
IP aliases added: 192.168.1.105, 192.168.1.108

Press Ctrl+C to stop.

2026/02/23 04:08:56 SSDP: sent initial NOTIFY for 3 printer(s)
2026/02/23 04:08:56 Camera: listening on 192.168.1.106:6000
2026/02/23 04:08:56 MQTT: listening on 192.168.1.106:8883
2026/02/23 04:08:56 MQTT: listening on 192.168.1.108:8883
2026/02/23 04:08:56 Camera: listening on 192.168.1.105:6000
2026/02/23 04:08:56 MQTT: listening on 192.168.1.105:8883
2026/02/23 04:08:56 Camera: listening on 192.168.1.108:6000
2026/02/23 04:08:56 SSDP: received M-SEARCH from 192.168.1.117:39825
```

### Data
```
openssl s_client -showcerts -connect 192.168.1.106:8883 </dev/null | sed -n -e '/-.BEGIN/,/-.END/ p' > blcert1.pem
mosquitto_sub -h 192.168.1.106 -p 8883 -u bblp -P '11258023'  --cafile blcert1.pem --insecure -t 'device/01P4F8E4EEA2CF1/report' -V mqttv311 |  jq .
{
  "print": {
    "ams": {
      "ams": [
        {
          "ams_id": "19F51A5827421H9",
          "check": 0,
          "chip_id": "",
          "dry_time": 0,
          "humidity": "3",
          "humidity_raw": "30",
          "id": "0",
          "info": "2004",
          "temp": "23.9",
          "tray": [
            {
              "bed_temp": "0",
              "bed_temp_type": "0",
              "cali_idx": -1,
              "cols": [
                "651A88FF"
              ],
              "ctype": 0,
              "id": "0",
              "k": 0.02,
              "n": 1,
              "nozzle_temp_max": "240",
              "nozzle_temp_min": "190",
              "remain": -1,
              "state": 3,
              "tag_uid": "0000000000000000",
              "total_len": 330000,
              "tray_color": "651A88FF",
              "tray_diameter": "0.00",
              "tray_id_name": "",
              "tray_info_idx": "GFL99",
              "tray_sub_brands": "",
              "tray_temp": "0",
              "tray_time": "0",
              "tray_type": "PLA",
              "tray_uuid": "00000000000000000000000000000000",
              "tray_weight": "0",
              "xcam_info": "000000000000000000000000"
            },
            {
              "bed_temp": "0",
              "bed_temp_type": "0",
              "cali_idx": -1,
              "cols": [
                "3B78E6FF"
              ],
              "ctype": 0,
              "id": "1",
              "k": 0.02,
              "n": 1,
              "nozzle_temp_max": "260",
              "nozzle_temp_min": "220",
              "remain": -1,
              "state": 3,
              "tag_uid": "0000000000000000",
              "total_len": 330000,
              "tray_color": "3B78E6FF",
              "tray_diameter": "0.00",
              "tray_id_name": "",
              "tray_info_idx": "GFG99",
              "tray_sub_brands": "",
              "tray_temp": "0",
              "tray_time": "0",
              "tray_type": "PETG",
              "tray_uuid": "00000000000000000000000000000000",
              "tray_weight": "0",
              "xcam_info": "000000000000000000000000"
            },
            {
              "bed_temp": "0",
              "bed_temp_type": "0",
              "cali_idx": -1,
              "cols": [
                "23901DFF"
              ],
              "ctype": 0,
              "id": "2",
              "k": 0.02,
              "n": 1,
              "nozzle_temp_max": "270",
              "nozzle_temp_min": "240",
              "remain": -1,
              "state": 3,
              "tag_uid": "0000000000000000",
              "total_len": 330000,
              "tray_color": "23901DFF",
              "tray_diameter": "0.00",
              "tray_id_name": "",
              "tray_info_idx": "GFA01",
              "tray_sub_brands": "",
              "tray_temp": "0",
              "tray_time": "0",
              "tray_type": "ABS",
              "tray_uuid": "00000000000000000000000000000000",
              "tray_weight": "0",
              "xcam_info": "000000000000000000000000"
            },
            {
              "bed_temp": "0",
              "bed_temp_type": "0",
              "cali_idx": -1,
              "cols": [
                "2360F0FF"
              ],
              "ctype": 0,
              "id": "3",
              "k": 0.02,
              "n": 1,
              "nozzle_temp_max": "240",
              "nozzle_temp_min": "200",
              "remain": -1,
              "state": 3,
              "tag_uid": "0000000000000000",
              "total_len": 330000,
              "tray_color": "2360F0FF",
              "tray_diameter": "0.00",
              "tray_id_name": "",
              "tray_info_idx": "GFU99",
              "tray_sub_brands": "",
              "tray_temp": "0",
              "tray_time": "0",
              "tray_type": "TPU",
              "tray_uuid": "00000000000000000000000000000000",
              "tray_weight": "0",
              "xcam_info": "000000000000000000000000"
            }
          ]
        }
      ],
      "ams_exist_bits": "1",
      "insert_flag": true,
      "power_on_flag": false,
      "tray_exist_bits": "F0",
      "tray_is_bbl_bits": "F0",
      "tray_now": "255",
      "tray_pre": "255",
      "tray_read_done_bits": "F0",
      "tray_reading_bits": "0",
      "tray_tar": "255",
      "version": 2
    },
    "ams_rfid_status": 0,
    "ams_status": 0,
    "bed_target_temper": 0,
    "bed_temper": 24,
    "big_fan1_speed": "0",
    "big_fan2_speed": "0",
    "cali_version": 0,
    "chamber_temper": 5,
    "command": "push_status",
    "cooling_fan_speed": "0",
    "fan_gear": 0,
    "filam_bak": [],
    "flag3": 8847,
    "force_upgrade": false,
    "gcode_file": "",
    "gcode_file_prepare_percent": "0",
    "gcode_state": "IDLE",
    "heatbreak_fan_speed": "0",
    "hms": [],
    "home_flag": 6505744,
    "hw_switch_state": 0,
    "ipcam": {
      "ipcam_dev": "1",
      "ipcam_record": "enable",
      "mode_bits": 3,
      "resolution": "",
      "timelapse": "disable",
      "tutk_server": "disable"
    },
    "k": "0.0200",
    "layer_num": 0,
    "lifecycle": "product",
    "lights_report": [
      {
        "mode": "off",
        "node": "chamber_light"
      }
    ],
    "mc_percent": 100,
    "mc_print_line_number": "0",
    "mc_print_stage": "1",
    "mc_print_sub_stage": 0,
    "mc_remaining_time": 0,
    "mess_production_state": "active",
    "msg": 0,
    "nozzle_diameter": "0.4",
    "nozzle_target_temper": 0,
    "nozzle_temper": 26.5,
    "nozzle_type": "hardened_steel",
    "online": {
      "ahb": false,
      "rfid": false,
      "version": 1271180554
    },
    "print_error": 0,
    "print_type": "idle",
    "profile_id": "0",
    "project_id": "0",
    "queue_est": 0,
    "queue_number": 0,
    "queue_sts": 0,
    "queue_total": 0,
    "s_obj": [],
    "sdcard": true,
    "sequence_id": "0",
    "spd_lvl": 2,
    "spd_mag": 100,
    "stg": [],
    "stg_cur": 255,
    "subtask_id": "0",
    "subtask_name": "",
    "task_id": "0",
    "total_layer_num": 0,
    "upgrade_state": {
      "consistency_request": false,
      "cur_state_code": 0,
      "dis_state": 0,
      "err_code": 0,
      "force_upgrade": false,
      "message": "0%, 0B/s",
      "module": "",
      "new_ver_list": [],
      "new_version_state": 0,
      "progress": "",
      "sequence_id": 0,
      "status": "IDLE"
    },
    "upload": {
      "message": "",
      "progress": 0,
      "status": "idle"
    },
    "vt_tray": {
      "bed_temp": "0",
      "bed_temp_type": "0",
      "cali_idx": -1,
      "id": "254",
      "k": 0.02,
      "n": 1,
      "nozzle_temp_max": "240",
      "nozzle_temp_min": "190",
      "remain": 0,
      "tag_uid": "0000000000000000",
      "tray_color": "78DD05FF",
      "tray_diameter": "0.00",
      "tray_id_name": "",
      "tray_info_idx": "GFL99",
      "tray_sub_brands": "",
      "tray_temp": "0",
      "tray_time": "0",
      "tray_type": "PLA",
      "tray_uuid": "00000000000000000000000000000000",
      "tray_weight": "0",
      "xcam_info": "000000000000000000000000"
    },
    "wifi_signal": "-48dBm"
  }
}
```
