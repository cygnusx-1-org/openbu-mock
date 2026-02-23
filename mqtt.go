package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"sync"
	"time"
)

func startMqtt(p *Printer) {
	tlsCert := generateSelfSignedCert(p.IP)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}

	addr := p.IP + ":8883"
	listener, err := tls.Listen("tcp", addr, tlsConfig)
	if err != nil {
		log.Fatalf("MQTT: failed to listen on %s: %v", addr, err)
	}
	defer listener.Close()

	log.Printf("MQTT: listening on %s", addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("MQTT: accept error: %v", err)
			continue
		}
		go handleMqttConnection(conn, p)
	}
}

func handleMqttConnection(conn net.Conn, p *Printer) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	log.Printf("MQTT: new connection from %s", remote)

	// Read CONNECT packet
	packetType, payload, err := readMqttPacket(conn)
	if err != nil {
		log.Printf("MQTT [%s]: failed to read CONNECT: %v", remote, err)
		return
	}
	if packetType != 1 { // CONNECT
		log.Printf("MQTT [%s]: expected CONNECT, got type %d", remote, packetType)
		return
	}

	// Parse CONNECT
	username, password, err := parseConnect(payload)
	if err != nil {
		log.Printf("MQTT [%s]: invalid CONNECT: %v", remote, err)
		sendConnack(conn, 4) // bad credentials
		return
	}

	if username != "bblp" || password != p.AccessCode {
		log.Printf("MQTT [%s]: auth failed (user=%s)", remote, username)
		sendConnack(conn, 5) // not authorized
		return
	}

	// Send CONNACK (success)
	sendConnack(conn, 0)
	log.Printf("MQTT [%s]: authenticated", remote)

	// Read SUBSCRIBE
	packetType, payload, err = readMqttPacket(conn)
	if err != nil {
		log.Printf("MQTT [%s]: failed to read SUBSCRIBE: %v", remote, err)
		return
	}
	if packetType != 8 { // SUBSCRIBE
		log.Printf("MQTT [%s]: expected SUBSCRIBE, got type %d", remote, packetType)
		return
	}

	packetID, topic, err := parseSubscribe(payload)
	if err != nil {
		log.Printf("MQTT [%s]: invalid SUBSCRIBE: %v", remote, err)
		return
	}

	expectedTopic := fmt.Sprintf("device/%s/report", p.Serial)
	if topic != expectedTopic {
		log.Printf("MQTT [%s]: wrong topic %q (expected %q), closing", remote, topic, expectedTopic)
		return
	}

	// Send SUBACK
	sendSuback(conn, packetID)
	log.Printf("MQTT [%s]: subscribed to %s", remote, topic)

	// Connection is now established. Start push loop and read loop.
	var writeMu sync.Mutex
	done := make(chan struct{})

	// Push status periodically
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		// Send initial status
		publishStatus(conn, &writeMu, p)

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				publishStatus(conn, &writeMu, p)
			}
		}
	}()

	// Read loop for incoming PUBLISH and PINGREQ
	defer close(done)
	for {
		packetType, payload, err = readMqttPacket(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("MQTT [%s]: read error: %v", remote, err)
			}
			log.Printf("MQTT [%s]: disconnected", remote)
			return
		}

		switch packetType {
		case 3: // PUBLISH
			handleIncomingPublish(payload, p, conn, &writeMu, remote)
		case 12: // PINGREQ
			writeMu.Lock()
			conn.Write([]byte{0xD0, 0x00}) // PINGRESP
			writeMu.Unlock()
		case 14: // DISCONNECT
			log.Printf("MQTT [%s]: client disconnected", remote)
			return
		default:
			log.Printf("MQTT [%s]: unhandled packet type %d", remote, packetType)
		}
	}
}

func handleIncomingPublish(payload []byte, p *Printer, conn net.Conn, writeMu *sync.Mutex, remote string) {
	if len(payload) < 2 {
		return
	}
	topicLen := int(binary.BigEndian.Uint16(payload[:2]))
	if 2+topicLen > len(payload) {
		return
	}
	topic := string(payload[2 : 2+topicLen])
	msgData := payload[2+topicLen:]

	expectedRequest := fmt.Sprintf("device/%s/request", p.Serial)
	if topic != expectedRequest {
		log.Printf("MQTT [%s]: publish to unexpected topic %q", remote, topic)
		return
	}

	// Parse the JSON command
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(msgData, &msg); err != nil {
		log.Printf("MQTT [%s]: invalid JSON in publish: %v", remote, err)
		return
	}

	// Handle pushall
	if pushRaw, ok := msg["pushing"]; ok {
		var push map[string]string
		json.Unmarshal(pushRaw, &push)
		if push["command"] == "pushall" {
			log.Printf("MQTT [%s]: pushall requested", remote)
			publishStatus(conn, writeMu, p)
		}
		return
	}

	// Handle light control
	if sysRaw, ok := msg["system"]; ok {
		var sys map[string]any
		json.Unmarshal(sysRaw, &sys)
		if cmd, _ := sys["command"].(string); cmd == "ledctrl" {
			mode, _ := sys["led_mode"].(string)
			p.SetLight(mode == "on")
			log.Printf("MQTT [%s]: light set to %s", remote, mode)
			// Push updated status
			publishStatus(conn, writeMu, p)
		}
		return
	}
}

func publishStatus(conn net.Conn, writeMu *sync.Mutex, p *Printer) {
	topic := fmt.Sprintf("device/%s/report", p.Serial)
	statusJSON := p.StatusJSON()
	packet := buildPublishPacket(topic, statusJSON)
	writeMu.Lock()
	conn.Write(packet)
	writeMu.Unlock()
}

// --- MQTT packet reading ---

func readMqttPacket(conn net.Conn) (packetType int, payload []byte, err error) {
	conn.SetReadDeadline(time.Now().Add(120 * time.Second))

	header := make([]byte, 1)
	if _, err = io.ReadFull(conn, header); err != nil {
		return 0, nil, err
	}
	packetType = int(header[0]) >> 4

	remainLen, err := readRemainingLength(conn)
	if err != nil {
		return 0, nil, err
	}

	payload = make([]byte, remainLen)
	if remainLen > 0 {
		if _, err = io.ReadFull(conn, payload); err != nil {
			return 0, nil, err
		}
	}
	return packetType, payload, nil
}

func readRemainingLength(conn net.Conn) (int, error) {
	value := 0
	multiplier := 1
	for {
		b := make([]byte, 1)
		if _, err := io.ReadFull(conn, b); err != nil {
			return 0, err
		}
		value += int(b[0]&0x7F) * multiplier
		multiplier *= 128
		if b[0]&0x80 == 0 {
			break
		}
	}
	return value, nil
}

// --- MQTT packet parsing ---

func parseConnect(payload []byte) (username, password string, err error) {
	if len(payload) < 10 {
		return "", "", fmt.Errorf("CONNECT payload too short")
	}

	// Skip protocol name (2 bytes len + "MQTT")
	protoLen := int(binary.BigEndian.Uint16(payload[:2]))
	offset := 2 + protoLen // past protocol name

	if offset+4 > len(payload) {
		return "", "", fmt.Errorf("CONNECT payload too short after protocol name")
	}

	// Protocol level (1 byte), connect flags (1 byte), keep alive (2 bytes)
	connectFlags := payload[offset+1]
	offset += 4 // past level, flags, keep-alive

	hasUsername := connectFlags&0x80 != 0
	hasPassword := connectFlags&0x40 != 0

	// Client ID
	if offset+2 > len(payload) {
		return "", "", fmt.Errorf("missing client ID")
	}
	clientIDLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
	offset += 2 + clientIDLen

	// Username
	if hasUsername {
		if offset+2 > len(payload) {
			return "", "", fmt.Errorf("missing username")
		}
		usernameLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if offset+usernameLen > len(payload) {
			return "", "", fmt.Errorf("username truncated")
		}
		username = string(payload[offset : offset+usernameLen])
		offset += usernameLen
	}

	// Password
	if hasPassword {
		if offset+2 > len(payload) {
			return "", "", fmt.Errorf("missing password")
		}
		passwordLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if offset+passwordLen > len(payload) {
			return "", "", fmt.Errorf("password truncated")
		}
		password = string(payload[offset : offset+passwordLen])
	}

	return username, password, nil
}

func parseSubscribe(payload []byte) (packetID uint16, topic string, err error) {
	if len(payload) < 5 {
		return 0, "", fmt.Errorf("SUBSCRIBE payload too short")
	}
	packetID = binary.BigEndian.Uint16(payload[:2])
	topicLen := int(binary.BigEndian.Uint16(payload[2:4]))
	if 4+topicLen > len(payload) {
		return 0, "", fmt.Errorf("topic truncated")
	}
	topic = string(payload[4 : 4+topicLen])
	return packetID, topic, nil
}

// --- MQTT packet builders ---

func sendConnack(conn net.Conn, returnCode byte) {
	// Fixed header: 0x20 (CONNACK), remaining length: 2
	// Variable header: session present (0), return code
	conn.Write([]byte{0x20, 0x02, 0x00, returnCode})
}

func sendSuback(conn net.Conn, packetID uint16) {
	// Fixed header: 0x90 (SUBACK), remaining length: 3
	// Variable header: packet ID (2 bytes), return code: 0x00 (QoS 0)
	conn.Write([]byte{0x90, 0x03, byte(packetID >> 8), byte(packetID), 0x00})
}

func buildPublishPacket(topic string, payload []byte) []byte {
	topicBytes := []byte(topic)
	// Variable header: topic length (2) + topic + payload
	varHeader := make([]byte, 2+len(topicBytes))
	binary.BigEndian.PutUint16(varHeader[:2], uint16(len(topicBytes)))
	copy(varHeader[2:], topicBytes)

	body := append(varHeader, payload...)
	return wrapMqttPacket(0x30, body)
}

func wrapMqttPacket(fixedHeaderByte byte, body []byte) []byte {
	var packet []byte
	packet = append(packet, fixedHeaderByte)

	// Encode remaining length
	length := len(body)
	for {
		b := byte(length % 128)
		length /= 128
		if length > 0 {
			b |= 0x80
		}
		packet = append(packet, b)
		if length == 0 {
			break
		}
	}

	packet = append(packet, body...)
	return packet
}

// --- TLS certificate generation ---

func generateSelfSignedCert(ip string) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("Failed to generate TLS key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Bambu Lab Mock Printer"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP(ip)},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		log.Fatalf("Failed to create TLS certificate: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
}
