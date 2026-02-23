package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	mrand "math/rand"
	"sync"
)

// filamentInfo maps filament type to tray_info_idx, nozzle temp range, and default K value.
var filamentInfo = map[string]struct {
	InfoIdx       string
	NozzleTempMax string
	NozzleTempMin string
	K             float64
}{
	"PLA":    {"GFL99", "240", "190", 0.02},
	"PETG":   {"GFG99", "260", "220", 0.02},
	"ABS":    {"GFA01", "270", "240", 0.02},
	"TPU":    {"GFU99", "240", "200", 0.02},
	"ASA":    {"GFS01", "270", "240", 0.02},
	"PA-CF":  {"GFN03", "300", "260", 0.02},
	"PA6-CF": {"GFN05", "300", "260", 0.02},
	"PLA-CF": {"GFL50", "240", "190", 0.02},
	"PETG-CF": {"GFG50", "280", "240", 0.02},
}

type Tray struct {
	ID       string
	TrayType string
	Color    string // 8-char hex RRGGBBAA
}

type AmsUnit struct {
	Model string
	Trays []Tray
}

type VtTray struct {
	TrayType      string
	TrayInfoIdx   string
	Color         string
	NozzleTempMax string
	NozzleTempMin string
	K             float64
}

type Printer struct {
	Serial     string
	Model      string
	IP         string
	AccessCode string
	DevModel   string

	mu      sync.RWMutex
	lightOn bool
	ams     *AmsUnit
	vtTray  *VtTray
}

func NewPrinter(serial, model, ip, accessCode, amsModel, extSpool string) *Printer {
	p := &Printer{
		Serial:     serial,
		Model:      model,
		IP:         ip,
		AccessCode: accessCode,
		DevModel:   devModelCodes[model],
	}

	if amsModel != "" {
		trayCount := amsTrayCounts[amsModel]
		a := &AmsUnit{Model: amsModel}
		trayTypes := []string{"PLA", "PETG", "ABS", "TPU", "ASA", "PA-CF"}
		for i := 0; i < trayCount; i++ {
			// 30% chance of an empty tray
			if mrand.Intn(10) < 3 {
				a.Trays = append(a.Trays, Tray{
					ID: fmt.Sprintf("%d", i),
				})
			} else {
				a.Trays = append(a.Trays, Tray{
					ID:       fmt.Sprintf("%d", i),
					TrayType: trayTypes[i%len(trayTypes)],
					Color:    randomColor(),
				})
			}
		}
		p.ams = a
	}

	// Resolve external spool: "RANDOM" means pick randomly (50% empty, 50% loaded)
	if extSpool == "RANDOM" {
		filamentTypes := make([]string, 0, len(filamentInfo))
		for ft := range filamentInfo {
			filamentTypes = append(filamentTypes, ft)
		}
		// 50% chance of empty external spool
		if mrand.Intn(2) == 0 {
			extSpool = ""
		} else {
			extSpool = filamentTypes[mrand.Intn(len(filamentTypes))]
		}
	}
	if extSpool != "" {
		info := filamentInfo[extSpool]
		p.vtTray = &VtTray{
			TrayType:      extSpool,
			TrayInfoIdx:   info.InfoIdx,
			Color:         randomColor(),
			NozzleTempMax: info.NozzleTempMax,
			NozzleTempMin: info.NozzleTempMin,
			K:             info.K,
		}
	}

	return p
}

func (p *Printer) DeviceName() string {
	prefix := serialPrefixes[p.Model]
	last3 := p.Serial[len(p.Serial)-3:]
	return fmt.Sprintf("3DP-%s-%s", prefix, last3)
}

func (p *Printer) SetLight(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lightOn = on
}

func (p *Printer) LightOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lightOn
}

func (p *Printer) StatusJSON() []byte {
	p.mu.RLock()
	defer p.mu.RUnlock()

	lightMode := "off"
	if p.lightOn {
		lightMode = "on"
	}

	status := map[string]any{
		"print": map[string]any{
			"upgrade_state": map[string]any{
				"sequence_id":       0,
				"progress":          "",
				"status":            "IDLE",
				"consistency_request": false,
				"dis_state":         0,
				"err_code":          0,
				"force_upgrade":     false,
				"message":           "0%, 0B/s",
				"module":            "",
				"new_version_state": 0,
				"cur_state_code":    0,
				"new_ver_list":      []any{},
			},
			"ipcam": map[string]any{
				"ipcam_dev":    "1",
				"ipcam_record": "enable",
				"timelapse":    "disable",
				"resolution":   "",
				"tutk_server":  "disable",
				"mode_bits":    3,
			},
			"upload": map[string]any{
				"status":   "idle",
				"progress": 0,
				"message":  "",
			},
			"nozzle_temper":        26.5,
			"nozzle_target_temper": 0,
			"bed_temper":           24.0,
			"bed_target_temper":    0,
			"chamber_temper":       5,
			"mc_print_stage":       "1",
			"heatbreak_fan_speed":  "0",
			"cooling_fan_speed":    "0",
			"big_fan1_speed":       "0",
			"big_fan2_speed":       "0",
			"mc_percent":           100,
			"mc_remaining_time":    0,
			"ams_status":           0,
			"ams_rfid_status":      0,
			"hw_switch_state":      0,
			"spd_mag":              100,
			"spd_lvl":              2,
			"print_error":          0,
			"lifecycle":            "product",
			"wifi_signal":          "-48dBm",
			"gcode_state":          "IDLE",
			"gcode_file_prepare_percent": "0",
			"queue_number":         0,
			"queue_total":          0,
			"queue_est":            0,
			"queue_sts":            0,
			"project_id":           "0",
			"profile_id":           "0",
			"task_id":              "0",
			"subtask_id":           "0",
			"subtask_name":         "",
			"gcode_file":           "",
			"stg":                  []any{},
			"stg_cur":              255,
			"print_type":           "idle",
			"home_flag":            6505744,
			"mc_print_line_number": "0",
			"mc_print_sub_stage":   0,
			"sdcard":               true,
			"force_upgrade":        false,
			"mess_production_state": "active",
			"layer_num":            0,
			"total_layer_num":      0,
			"s_obj":                []any{},
			"filam_bak":            []any{},
			"fan_gear":             0,
			"nozzle_diameter":      "0.4",
			"nozzle_type":          "hardened_steel",
			"cali_version":         0,
			"k":                    "0.0200",
			"flag3":                8847,
			"hms":                  []any{},
			"online": map[string]any{
				"ahb":     false,
				"rfid":    false,
				"version": 1271180554,
			},
			"vt_tray": p.buildVtTray(),
			"lights_report": []map[string]any{
				{
					"node": "chamber_light",
					"mode": lightMode,
				},
			},
			"command":     "push_status",
			"msg":         0,
			"sequence_id": "0",
		},
	}

	// Add AMS data if configured
	printMap := status["print"].(map[string]any)
	if p.ams != nil {
		trays := make([]map[string]any, len(p.ams.Trays))
		trayExistBits := 0
		for i, t := range p.ams.Trays {
			if t.TrayType == "" {
				// Empty tray — just the id
				trays[i] = map[string]any{
					"id": t.ID,
				}
			} else {
				trayExistBits |= 1 << (i + 4)
				info := filamentInfo[t.TrayType]
				trays[i] = map[string]any{
					"id":              t.ID,
					"state":           3,
					"remain":          -1,
					"k":               info.K,
					"n":               1,
					"cali_idx":        -1,
					"total_len":       330000,
					"tag_uid":         "0000000000000000",
					"tray_id_name":    "",
					"tray_info_idx":   info.InfoIdx,
					"tray_type":       t.TrayType,
					"tray_sub_brands": "",
					"tray_color":      t.Color,
					"tray_weight":     "0",
					"tray_diameter":   "0.00",
					"tray_temp":       "0",
					"tray_time":       "0",
					"bed_temp_type":   "0",
					"bed_temp":        "0",
					"nozzle_temp_max": info.NozzleTempMax,
					"nozzle_temp_min": info.NozzleTempMin,
					"xcam_info":       "000000000000000000000000",
					"tray_uuid":       "00000000000000000000000000000000",
					"ctype":           0,
					"cols":            []string{t.Color},
				}
			}
		}

		// AMS-HT uses id 128+, AMS and AMS-2-PRO use id 0-3
		amsID := "0"
		if p.ams.Model == "AMS-HT" {
			amsID = "128"
		}

		amsUnit := map[string]any{
			"chip_id":      "",
			"ams_id":       fmt.Sprintf("19F51A5827000H%d", 8),
			"check":        0,
			"id":           amsID,
			"humidity":     "3",
			"humidity_raw": "30",
			"temp":         "23.9",
			"info":         "2004",
			"tray":         trays,
		}

		// AMS-2-PRO and AMS-HT support drying; AMS does not
		if p.ams.Model == "AMS-2-PRO" || p.ams.Model == "AMS-HT" {
			amsUnit["dry_time"] = 0
		}

		printMap["ams"] = map[string]any{
			"ams":                 []map[string]any{amsUnit},
			"ams_exist_bits":      "1",
			"tray_exist_bits":     fmt.Sprintf("%X", trayExistBits),
			"tray_is_bbl_bits":    fmt.Sprintf("%X", trayExistBits),
			"tray_tar":           "255",
			"tray_now":           "255",
			"tray_pre":           "255",
			"tray_read_done_bits": fmt.Sprintf("%X", trayExistBits),
			"tray_reading_bits":  "0",
			"version":            2,
			"insert_flag":        true,
			"power_on_flag":      false,
		}
	}

	data, _ := json.Marshal(status)
	return data
}

func (p *Printer) buildVtTray() map[string]any {
	vt := map[string]any{
		"id":              "254",
		"tag_uid":         "0000000000000000",
		"tray_id_name":    "",
		"tray_info_idx":   "",
		"tray_type":       "",
		"tray_sub_brands": "",
		"tray_color":      "FFFFFF00",
		"tray_weight":     "0",
		"tray_diameter":   "0.00",
		"tray_temp":       "0",
		"tray_time":       "0",
		"bed_temp_type":   "0",
		"bed_temp":        "0",
		"nozzle_temp_max": "0",
		"nozzle_temp_min": "0",
		"xcam_info":       "000000000000000000000000",
		"tray_uuid":       "00000000000000000000000000000000",
		"remain":          0,
		"k":               0.02,
		"n":               1,
		"cali_idx":        -1,
	}
	if p.vtTray != nil {
		vt["tray_type"] = p.vtTray.TrayType
		vt["tray_info_idx"] = p.vtTray.TrayInfoIdx
		vt["tray_color"] = p.vtTray.Color
		vt["nozzle_temp_max"] = p.vtTray.NozzleTempMax
		vt["nozzle_temp_min"] = p.vtTray.NozzleTempMin
		vt["k"] = p.vtTray.K
	}
	return vt
}

func randomColor() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%02X%02X%02XFF", b[0], b[1], b[2])
}
