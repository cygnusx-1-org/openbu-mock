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
	"PLA":     {"GFL99", "240", "190", 0.02},
	"PETG":    {"GFG99", "260", "220", 0.02},
	"ABS":     {"GFA01", "270", "240", 0.02},
	"TPU":     {"GFU99", "240", "200", 0.02},
	"ASA":     {"GFS01", "270", "240", 0.02},
	"PA-CF":   {"GFN03", "300", "260", 0.02},
	"PA6-CF":  {"GFN05", "300", "260", 0.02},
	"PLA-CF":  {"GFL50", "240", "190", 0.02},
	"PETG-CF": {"GFG50", "280", "240", 0.02},
}

type Tray struct {
	ID       string
	TrayType string
	Color    string // 8-char hex RRGGBBAA
}

type AmsUnit struct {
	ID    int // 0-3 for AMS/AMS-2-PRO, 128+ for AMS-HT
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
	ams     []*AmsUnit
	vtTray  *VtTray
}

// AmsSpec describes a requested AMS unit type and count.
type AmsSpec struct {
	Model string
	Count int
}

func NewPrinter(serial, model, ip, accessCode string, amsSpecs []AmsSpec, extSpool string) *Printer {
	p := &Printer{
		Serial:     serial,
		Model:      model,
		IP:         ip,
		AccessCode: accessCode,
		DevModel:   devModelCodes[model],
	}

	// Assign AMS IDs: AMS-HT starts at 128, others start at 0
	nextRegularID := 0
	nextHTID := 128
	trayTypes := []string{"PLA", "PETG", "ABS", "TPU", "ASA", "PA-CF"}

	for _, spec := range amsSpecs {
		trayCount := amsTrayCounts[spec.Model]
		for n := 0; n < spec.Count; n++ {
			id := nextRegularID
			if spec.Model == "AMS-HT" {
				id = nextHTID
				nextHTID++
			} else {
				nextRegularID++
			}

			a := &AmsUnit{ID: id, Model: spec.Model}
			for i := 0; i < trayCount; i++ {
				// 30% chance of an empty tray
				if mrand.Intn(10) < 3 {
					a.Trays = append(a.Trays, Tray{
						ID: fmt.Sprintf("%d", i),
					})
				} else {
					a.Trays = append(a.Trays, Tray{
						ID:       fmt.Sprintf("%d", i),
						TrayType: trayTypes[(id*4+i)%len(trayTypes)],
						Color:    randomColor(),
					})
				}
			}
			p.ams = append(p.ams, a)
		}
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
			"upgrade_state": p.buildUpgradeState(),
			"ipcam": p.buildIpcam(),
			"upload": p.buildUpload(),
			"nozzle_temper":              26.5,
			"nozzle_target_temper":       0,
			"bed_temper":                 24.0,
			"bed_target_temper":          0,
			"chamber_temper":             5,
			"mc_print_stage":             "1",
			"heatbreak_fan_speed":        "0",
			"cooling_fan_speed":          "0",
			"big_fan1_speed":             "0",
			"big_fan2_speed":             "0",
			"mc_percent":                 100,
			"mc_remaining_time":          0,
			"ams_status":                 0,
			"ams_rfid_status":            0,
			"hw_switch_state":            0,
			"spd_mag":                    100,
			"spd_lvl":                    2,
			"print_error":                0,
			"lifecycle":                  "product",
			"wifi_signal":                "-48dBm",
			"gcode_state":                "IDLE",
			"gcode_file_prepare_percent": "0",
			"queue_number":               0,
			"queue_total":                0,
			"queue_est":                  0,
			"queue_sts":                  0,
			"project_id":                 "0",
			"profile_id":                 "0",
			"task_id":                    "0",
			"subtask_id":                 "0",
			"subtask_name":               "",
			"gcode_file":                 "",
			"stg":                        []any{},
			"stg_cur":                    255,
			"print_type":                 "idle",
			"home_flag":                  6505744,
			"mc_print_line_number":       "0",
			"mc_print_sub_stage":         0,
			"sdcard":                     true,
			"force_upgrade":              false,
			"mess_production_state":      "active",
			"layer_num":                  0,
			"total_layer_num":            0,
			"s_obj":                      []any{},
			"filam_bak":                  []any{},
			"fan_gear":                   0,
			"nozzle_diameter":            "0.4",
			"nozzle_type":                p.nozzleType(),
			"cali_version":               0,
			"k":                          "0.0200",
			"flag3":                      8847,
			"hms":                        []any{},
			"online": p.buildOnline(),
			"vt_tray": p.buildVtTray(),
			"lights_report": p.buildLightsReport(lightMode),
			"command":     "push_status",
			"msg":         0,
			"sequence_id": "0",
		},
	}

	// Always emit AMS block (real printers send it even with no AMS units)
	printMap := status["print"].(map[string]any)
	var amsUnits []map[string]any
	amsExistBits := 0
	trayExistBits := 0

	for _, a := range p.ams {
		trays := make([]map[string]any, len(a.Trays))
		for i, t := range a.Trays {
			if t.TrayType == "" {
				trays[i] = map[string]any{
					"id": t.ID,
				}
			} else {
				// Tray bit position: for regular AMS (id 0-3), shift by (id+1)*4 + trayIdx
				// For AMS-HT, use a separate range
				bitPos := (a.ID+1)*4 + i
				trayExistBits |= 1 << bitPos
				info := filamentInfo[t.TrayType]
				tray := map[string]any{
					"id":              t.ID,
					"remain":          -1,
					"cali_idx":        -1,
					"tag_uid":         "0000000000000000",
					"tray_id_name":    "",
					"tray_info_idx":   info.InfoIdx,
					"tray_type":       t.TrayType,
					"tray_sub_brands": "",
					"tray_color":      t.Color,
					"tray_weight":     "0",
					"tray_diameter":   "0.00",
					"bed_temp_type":   "0",
					"bed_temp":        "0",
					"nozzle_temp_max": info.NozzleTempMax,
					"nozzle_temp_min": info.NozzleTempMin,
					"xcam_info":       "000000000000000000000000",
					"tray_uuid":       "00000000000000000000000000000000",
				}
				if p.isX1Family() {
					tray["drying_temp"] = "0"
					tray["drying_time"] = "0"
					tray["ctype"] = 0
					tray["cols"] = []string{t.Color}
				} else {
					tray["k"] = info.K
					tray["n"] = 1
					tray["state"] = 3
					tray["total_len"] = 330000
					tray["tray_temp"] = "0"
					tray["tray_time"] = "0"
				}
				trays[i] = tray
			}
		}

		amsExistBits |= 1 << a.ID

		amsUnit := map[string]any{
			"id":       fmt.Sprintf("%d", a.ID),
			"humidity": "3",
			"temp":     "23.9",
			"tray":     trays,
		}
		amsUnits = append(amsUnits, amsUnit)
	}

	amsVersion := 1
	if len(p.ams) > 0 {
		amsVersion = 2
	}

	printMap["ams"] = map[string]any{
		"ams":                 amsUnits,
		"ams_exist_bits":      fmt.Sprintf("%x", amsExistBits),
		"tray_exist_bits":     fmt.Sprintf("%x", trayExistBits),
		"tray_is_bbl_bits":    fmt.Sprintf("%x", trayExistBits),
		"tray_tar":            "255",
		"tray_now":            "255",
		"tray_pre":            "255",
		"tray_read_done_bits": fmt.Sprintf("%x", trayExistBits),
		"tray_reading_bits":   "0",
		"version":             amsVersion,
		"insert_flag":         true,
		"power_on_flag":       false,
	}

	data, _ := json.Marshal(status)
	return data
}

func (p *Printer) isX1Family() bool {
	return p.Model == "X1" || p.Model == "X1C" || p.Model == "X1E"
}

func (p *Printer) nozzleType() string {
	if p.Model == "A1-MINI" || p.Model == "A1" {
		return "stainless_steel"
	}
	return "hardened_steel"
}

const (
	rtspUsername = "bblp"
	rtspPort     = 322
	rtspPath     = "/streaming/live/1"
)

func (p *Printer) buildIpcam() map[string]any {
	if p.Model == "A1-MINI" || p.Model == "A1" {
		return map[string]any{
			"ipcam_dev":    "1",
			"ipcam_record": "enable",
			"timelapse":    "disable",
			"resolution":   "1080p",
			"tutk_server":  "disable",
			"mode_bits":    3,
		}
	}
	if p.Model == "P1P" || p.Model == "P1S" {
		return map[string]any{
			"ipcam_dev":    "1",
			"ipcam_record": "enable",
			"timelapse":    "disable",
			"resolution":   "",
			"tutk_server":  "disable",
			"mode_bits":    3,
		}
	}
	return map[string]any{
		"ipcam_dev":    "1",
		"ipcam_record": "enable",
		"mode_bits":    2,
		"resolution":   "1080p",
		"rtsp_url":     fmt.Sprintf("rtsps://%s:%s@%s:%d%s", rtspUsername, p.AccessCode, p.IP, rtspPort, rtspPath),
		"timelapse":    "disable",
		"tutk_server":  "enable",
	}
}

func (p *Printer) buildVtTray() map[string]any {
	color := "00000000"
	trayType := ""
	trayInfoIdx := ""
	nozzleTempMax := "0"
	nozzleTempMin := "0"
	k := 0.02
	if p.vtTray != nil {
		color = p.vtTray.Color
		trayType = p.vtTray.TrayType
		trayInfoIdx = p.vtTray.TrayInfoIdx
		nozzleTempMax = p.vtTray.NozzleTempMax
		nozzleTempMin = p.vtTray.NozzleTempMin
		k = p.vtTray.K
	}
	vt := map[string]any{
		"id":              "254",
		"tag_uid":         "0000000000000000",
		"tray_id_name":    "",
		"tray_info_idx":   trayInfoIdx,
		"tray_type":       trayType,
		"tray_sub_brands": "",
		"tray_color":      color,
		"tray_weight":     "0",
		"tray_diameter":   "0.00",
		"bed_temp_type":   "0",
		"bed_temp":        "0",
		"nozzle_temp_max": nozzleTempMax,
		"nozzle_temp_min": nozzleTempMin,
		"xcam_info":       "000000000000000000000000",
		"tray_uuid":       "00000000000000000000000000000000",
		"remain":          0,
		"cali_idx":        -1,
	}
	if p.isX1Family() {
		vt["cols"] = []string{color}
		vt["ctype"] = 0
		vt["drying_temp"] = "0"
		vt["drying_time"] = "0"
	} else {
		vt["tray_temp"] = "0"
		vt["tray_time"] = "0"
		vt["k"] = k
		vt["n"] = 1
	}
	return vt
}

func (p *Printer) buildUpgradeState() map[string]any {
	if p.isX1Family() {
		return map[string]any{
			"sequence_id":              0,
			"progress":                 "0",
			"status":                   "IDLE",
			"consistency_request":      false,
			"dis_state":                0,
			"err_code":                 0,
			"force_upgrade":            false,
			"message":                  "",
			"module":                   "",
			"new_version_state":        2,
			"ahb_new_version_number":   "",
			"ams_new_version_number":   "",
			"ext_new_version_number":   "",
			"ota_new_version_number":   "",
			"idx":                      0,
			"sn":                       p.Serial,
		}
	}
	return map[string]any{
		"sequence_id":         0,
		"progress":            "",
		"status":              "IDLE",
		"consistency_request": false,
		"dis_state":           0,
		"err_code":            0,
		"force_upgrade":       false,
		"message":             "0%, 0B/s",
		"module":              "",
		"new_version_state":   0,
		"cur_state_code":      0,
		"new_ver_list":        []any{},
	}
}

func (p *Printer) buildUpload() map[string]any {
	if p.isX1Family() {
		return map[string]any{
			"status":         "idle",
			"progress":       0,
			"message":        "Good",
			"file_size":      0,
			"finish_size":    0,
			"oss_url":        "",
			"sequence_id":    "0903",
			"speed":          0,
			"task_id":        "",
			"time_remaining": 0,
			"trouble_id":     "",
		}
	}
	return map[string]any{
		"status":   "idle",
		"progress": 0,
		"message":  "",
	}
}

func (p *Printer) buildOnline() map[string]any {
	if p.isX1Family() {
		return map[string]any{
			"ahb":     false,
			"ext":     false,
			"version": 7,
		}
	}
	return map[string]any{
		"ahb":     false,
		"rfid":    false,
		"version": 1271180554,
	}
}

func (p *Printer) isH2Family() bool {
	return p.Model == "H2C" || p.Model == "H2D" || p.Model == "H2D-PRO" || p.Model == "H2S"
}

func (p *Printer) buildLightsReport(lightMode string) []map[string]any {
	report := []map[string]any{
		{
			"node": "chamber_light",
			"mode": lightMode,
		},
	}
	if p.isX1Family() || p.isH2Family() {
		report = append(report, map[string]any{
			"node": "work_light",
			"mode": "flashing",
		})
	}
	if p.Model == "H2C" || p.Model == "H2D" {
		report = append(report, map[string]any{
			"node": "chamber_light2",
			"mode": lightMode,
		})
	}
	return report
}

// AMS module metadata for get_version responses.
var amsModuleMeta = map[string]struct {
	NamePrefix  string
	HwVer       string
	ProductName string
}{
	"AMS":       {"ams", "AMS08", "AMS"},
	"AMS-2-PRO": {"n3f", "N3F05", "AMS 2 Pro"},
	"AMS-HT":    {"n3s", "N3S05", "AMS HT"},
}

// Printer product names for the ota module.
var printerProductNames = map[string]string{
	"A1-MINI": "Bambu Lab A1 mini",
	"A1":      "Bambu Lab A1",
	"H2C":     "Bambu Lab H2C",
	"H2D":     "Bambu Lab H2D",
	"H2D-PRO": "Bambu Lab H2D Pro",
	"H2S":     "Bambu Lab H2S",
	"P1P":     "Bambu Lab P1P",
	"P1S":     "Bambu Lab P1S",
	"P2S":     "Bambu Lab P2S",
	"X1":      "Bambu Lab X1",
	"X1C":     "Bambu Lab X1-Carbon",
	"X1E":     "Bambu Lab X1E",
}

func (p *Printer) VersionJSON(sequenceID string) []byte {
	modules := []map[string]any{
		{
			"name":         "ota",
			"project_name": "",
			"sw_ver":       "01.09.01.00",
			"hw_ver":       "",
			"sn":           p.Serial,
			"flag":         0,
			"product_name": printerProductNames[p.Model],
			"visible":      true,
		},
		{
			"name":         "mc",
			"project_name": "",
			"sw_ver":       "11.0302.00.98",
			"hw_ver":       "",
			"sn":           "",
			"flag":         0,
			"product_name": "",
			"visible":      false,
		},
		{
			"name":         "th",
			"project_name": "",
			"sw_ver":       "00.00.06.02",
			"hw_ver":       "",
			"sn":           "",
			"flag":         0,
			"product_name": "",
			"visible":      false,
		},
	}

	if p.Model == "A1-MINI" || p.Model == "A1" {
		modules = append(modules, map[string]any{
			"name":         "esp32",
			"project_name": "",
			"sw_ver":       "01.11.33.52",
			"hw_ver":       "AP07",
			"sn":           "",
			"flag":         0,
		})
	}

	hasHub := false
	for i, a := range p.ams {
		meta := amsModuleMeta[a.Model]

		modules = append(modules, map[string]any{
			"name":         fmt.Sprintf("%s/%d", meta.NamePrefix, a.ID),
			"project_name": "",
			"sw_ver":       "00.00.06.38",
			"hw_ver":       meta.HwVer,
			"sn":           "",
			"flag":         0,
			"product_name": fmt.Sprintf("%s (%d)", meta.ProductName, i+1),
			"visible":      true,
		})

		if a.Model != "AMS-HT" {
			hasHub = true
		}
	}

	// AMS Hub needed when any non-HT AMS is present
	if hasHub {
		modules = append(modules, map[string]any{
			"name":         "ahb",
			"project_name": "",
			"sw_ver":       "00.02.00.58",
			"hw_ver":       "",
			"sn":           "",
			"flag":         0,
			"product_name": "",
			"visible":      true,
		})
	}

	resp := map[string]any{
		"info": map[string]any{
			"command":     "get_version",
			"sequence_id": sequenceID,
			"module":      modules,
		},
	}

	data, _ := json.Marshal(resp)
	return data
}

func randomColor() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%02X%02X%02XFF", b[0], b[1], b[2])
}
