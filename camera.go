package main

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log"
	"net"
	"time"
)

func startCamera(p *Printer) {
	tlsCert := generateSelfSignedCert()
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}

	addr := p.IP + ":6000"
	listener, err := tls.Listen("tcp", addr, tlsConfig)
	if err != nil {
		log.Fatalf("Camera: failed to listen on %s: %v", addr, err)
	}
	defer listener.Close()

	log.Printf("Camera: listening on %s", addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Camera: accept error: %v", err)
			continue
		}
		go handleCameraConnection(conn, p)
	}
}

func handleCameraConnection(conn net.Conn, p *Printer) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	log.Printf("Camera [%s]: new connection", remote)

	// Read 80-byte auth payload
	auth := make([]byte, 80)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(conn, auth); err != nil {
		log.Printf("Camera [%s]: failed to read auth: %v", remote, err)
		return
	}

	// Validate auth header
	magic := binary.LittleEndian.Uint32(auth[0:4])
	if magic != 0x40 {
		log.Printf("Camera [%s]: invalid auth magic: 0x%X", remote, magic)
		return
	}

	// Extract username (bytes 16-47) and access code (bytes 48-79)
	username := string(bytes.TrimRight(auth[16:48], "\x00"))
	code := string(bytes.TrimRight(auth[48:80], "\x00"))

	if username != "bblp" || code != p.AccessCode {
		log.Printf("Camera [%s]: auth failed (user=%s)", remote, username)
		return
	}

	log.Printf("Camera [%s]: authenticated, streaming", remote)
	conn.SetReadDeadline(time.Time{}) // clear deadline

	// Stream JPEG frames at ~10 fps, regenerating each frame to reflect current state
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		frame := generateTestFrame(p)
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(frame); err != nil {
			log.Printf("Camera [%s]: write error, disconnecting", remote)
			return
		}
	}
}

// generateTestFrame creates a minimal JPEG image with printer info text.
func generateTestFrame(p *Printer) []byte {
	width, height := 640, 480
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	lightOn := p.LightOn()

	// Background: bright when light on, dark when off
	var bg color.RGBA
	if lightOn {
		bg = color.RGBA{R: 80, G: 80, B: 70, A: 255}
	} else {
		bg = color.RGBA{R: 15, G: 15, B: 15, A: 255}
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, bg)
		}
	}

	// Draw a simple crosshair pattern so it's obvious it's a test image
	var crossColor color.RGBA
	if lightOn {
		crossColor = color.RGBA{R: 0, G: 220, B: 0, A: 255}
	} else {
		crossColor = color.RGBA{R: 0, G: 80, B: 0, A: 255}
	}
	for x := 0; x < width; x++ {
		img.Set(x, height/2, crossColor)
	}
	for y := 0; y < height; y++ {
		img.Set(width/2, y, crossColor)
	}

	// Draw border
	for x := 0; x < width; x++ {
		img.Set(x, 0, crossColor)
		img.Set(x, height-1, crossColor)
	}
	for y := 0; y < height; y++ {
		img.Set(0, y, crossColor)
		img.Set(width-1, y, crossColor)
	}

	// Draw text
	var textColor, dimColor color.RGBA
	if lightOn {
		textColor = color.RGBA{R: 255, G: 255, B: 255, A: 255}
		dimColor = color.RGBA{R: 220, G: 220, B: 220, A: 255}
	} else {
		textColor = color.RGBA{R: 140, G: 140, B: 140, A: 255}
		dimColor = color.RGBA{R: 90, G: 90, B: 90, A: 255}
	}
	drawBlockText(img, fmt.Sprintf("MOCK %s", p.Model), width/2, height/2-60, textColor)
	drawBlockText(img, p.Serial, width/2, height/2, dimColor)

	// Light status indicator
	lightLabel := "LIGHT OFF"
	lightColor := color.RGBA{R: 120, G: 0, B: 0, A: 255}
	if lightOn {
		lightLabel = "LIGHT ON"
		lightColor = color.RGBA{R: 255, G: 220, B: 50, A: 255}
	}
	drawBlockText(img, lightLabel, width/2, height/2+50, lightColor)

	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75})
	raw := buf.Bytes()

	// Go's jpeg.Encode does not produce a JFIF APP0 marker (FF E0).
	// The Bambu camera client looks for FF D8 FF E0 as the SOI signature.
	// Insert a minimal JFIF APP0 segment right after the SOI marker (FF D8).
	jfifApp0 := []byte{
		0xFF, 0xE0, // APP0 marker
		0x00, 0x10, // Length: 16 bytes
		'J', 'F', 'I', 'F', 0x00, // Identifier
		0x01, 0x01, // Version 1.1
		0x00,       // Aspect ratio units: no units
		0x00, 0x01, // X density
		0x00, 0x01, // Y density
		0x00, 0x00, // No thumbnail
	}

	// raw[0:2] is FF D8 (SOI), rest follows
	var out bytes.Buffer
	out.Write(raw[:2])   // SOI
	out.Write(jfifApp0)  // JFIF APP0
	out.Write(raw[2:])   // rest of JPEG
	return out.Bytes()
}

// drawBlockText draws simple block-pixel text centered at (cx, cy).
func drawBlockText(img *image.RGBA, text string, cx, cy int, c color.RGBA) {
	// Simple 5x7 pixel font for uppercase + digits + space + dash
	glyphs := map[byte][7]uint8{
		'A': {0x1C, 0x22, 0x22, 0x3E, 0x22, 0x22, 0x22},
		'B': {0x3C, 0x22, 0x22, 0x3C, 0x22, 0x22, 0x3C},
		'C': {0x1C, 0x22, 0x20, 0x20, 0x20, 0x22, 0x1C},
		'D': {0x3C, 0x22, 0x22, 0x22, 0x22, 0x22, 0x3C},
		'E': {0x3E, 0x20, 0x20, 0x3C, 0x20, 0x20, 0x3E},
		'F': {0x3E, 0x20, 0x20, 0x3C, 0x20, 0x20, 0x20},
		'G': {0x1C, 0x22, 0x20, 0x2E, 0x22, 0x22, 0x1C},
		'H': {0x22, 0x22, 0x22, 0x3E, 0x22, 0x22, 0x22},
		'I': {0x1C, 0x08, 0x08, 0x08, 0x08, 0x08, 0x1C},
		'J': {0x0E, 0x04, 0x04, 0x04, 0x04, 0x24, 0x18},
		'K': {0x22, 0x24, 0x28, 0x30, 0x28, 0x24, 0x22},
		'L': {0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x3E},
		'M': {0x22, 0x36, 0x2A, 0x2A, 0x22, 0x22, 0x22},
		'N': {0x22, 0x32, 0x2A, 0x26, 0x22, 0x22, 0x22},
		'O': {0x1C, 0x22, 0x22, 0x22, 0x22, 0x22, 0x1C},
		'P': {0x3C, 0x22, 0x22, 0x3C, 0x20, 0x20, 0x20},
		'Q': {0x1C, 0x22, 0x22, 0x22, 0x2A, 0x24, 0x1A},
		'R': {0x3C, 0x22, 0x22, 0x3C, 0x28, 0x24, 0x22},
		'S': {0x1C, 0x22, 0x20, 0x1C, 0x02, 0x22, 0x1C},
		'T': {0x3E, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08},
		'U': {0x22, 0x22, 0x22, 0x22, 0x22, 0x22, 0x1C},
		'V': {0x22, 0x22, 0x22, 0x22, 0x22, 0x14, 0x08},
		'W': {0x22, 0x22, 0x22, 0x2A, 0x2A, 0x36, 0x22},
		'X': {0x22, 0x22, 0x14, 0x08, 0x14, 0x22, 0x22},
		'Y': {0x22, 0x22, 0x14, 0x08, 0x08, 0x08, 0x08},
		'Z': {0x3E, 0x02, 0x04, 0x08, 0x10, 0x20, 0x3E},
		'0': {0x1C, 0x22, 0x26, 0x2A, 0x32, 0x22, 0x1C},
		'1': {0x08, 0x18, 0x08, 0x08, 0x08, 0x08, 0x1C},
		'2': {0x1C, 0x22, 0x02, 0x0C, 0x10, 0x20, 0x3E},
		'3': {0x1C, 0x22, 0x02, 0x0C, 0x02, 0x22, 0x1C},
		'4': {0x04, 0x0C, 0x14, 0x24, 0x3E, 0x04, 0x04},
		'5': {0x3E, 0x20, 0x3C, 0x02, 0x02, 0x22, 0x1C},
		'6': {0x1C, 0x20, 0x20, 0x3C, 0x22, 0x22, 0x1C},
		'7': {0x3E, 0x02, 0x04, 0x08, 0x10, 0x10, 0x10},
		'8': {0x1C, 0x22, 0x22, 0x1C, 0x22, 0x22, 0x1C},
		'9': {0x1C, 0x22, 0x22, 0x1E, 0x02, 0x02, 0x1C},
		' ': {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		'-': {0x00, 0x00, 0x00, 0x1C, 0x00, 0x00, 0x00},
	}

	scale := 3
	charW := 6 * scale // 5 pixels + 1 gap
	totalW := len(text) * charW
	startX := cx - totalW/2

	for ci, ch := range text {
		glyph, ok := glyphs[byte(ch)]
		if !ok {
			continue
		}
		ox := startX + ci*charW
		for row := 0; row < 7; row++ {
			for col := 0; col < 6; col++ {
				if glyph[row]&(1<<(5-col)) != 0 {
					for sy := 0; sy < scale; sy++ {
						for sx := 0; sx < scale; sx++ {
							px := ox + col*scale + sx
							py := cy + row*scale + sy
							if px >= 0 && px < img.Bounds().Max.X && py >= 0 && py < img.Bounds().Max.Y {
								img.Set(px, py, c)
							}
						}
					}
				}
			}
		}
	}
}
