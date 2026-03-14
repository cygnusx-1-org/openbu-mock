package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// tlsVersionName returns a human-readable TLS version string.
func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("unknown(0x%04x)", v)
	}
}

// tlsCipherSuiteName returns a human-readable cipher suite name.
func tlsCipherSuiteName(id uint16) string {
	for _, s := range tls.CipherSuites() {
		if s.ID == id {
			return s.Name
		}
	}
	for _, s := range tls.InsecureCipherSuites() {
		if s.ID == id {
			return s.Name
		}
	}
	return fmt.Sprintf("unknown(0x%04x)", id)
}

func tlsExtensionName(t uint16) string {
	switch t {
	case 0x0000:
		return "server_name"
	case 0x000B:
		return "ec_point_formats"
	case 0x000D:
		return "signature_algorithms"
	case 0x0017:
		return "extended_master_secret"
	case 0x002B:
		return "supported_versions"
	case 0xFF01:
		return "renegotiation_info"
	default:
		return fmt.Sprintf("ext_%04x", t)
	}
}

func tlsSignatureSchemeName(s tls.SignatureScheme) string {
	switch s {
	case tls.PKCS1WithSHA256:
		return "RSA_PKCS1_SHA256"
	case tls.PKCS1WithSHA384:
		return "RSA_PKCS1_SHA384"
	case tls.PKCS1WithSHA512:
		return "RSA_PKCS1_SHA512"
	case tls.PSSWithSHA256:
		return "RSA_PSS_SHA256"
	case tls.PSSWithSHA384:
		return "RSA_PSS_SHA384"
	case tls.PSSWithSHA512:
		return "RSA_PSS_SHA512"
	case tls.ECDSAWithP256AndSHA256:
		return "ECDSA_P256_SHA256"
	case tls.ECDSAWithP384AndSHA384:
		return "ECDSA_P384_SHA384"
	case tls.ECDSAWithP521AndSHA512:
		return "ECDSA_P521_SHA512"
	case tls.Ed25519:
		return "Ed25519"
	case 0x0201:
		return "RSA_PKCS1_SHA1"
	case 0x0203:
		return "ECDSA_SHA1"
	default:
		return fmt.Sprintf("unknown(0x%04x)", uint16(s))
	}
}

func startMqtt(ca *CA, p *Printer) {
	tlsCert := generateCertChain(ca, p.Serial, p.IP)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		// Use P-256 for ECDHE key exchange — older clients (OpenSSL 1.0.x)
		// don't support X25519 and will drop the connection during handshake.
		CurvePreferences: []tls.CurveID{tls.CurveP256, tls.CurveP384},
	}

	if *debug {
		// Log the server certificate details
		if parsed, err := x509.ParseCertificate(tlsCert.Certificate[0]); err == nil {
			var ipSANs []string
			for _, ip := range parsed.IPAddresses {
				ipSANs = append(ipSANs, ip.String())
			}
			log.Printf("MQTT: server cert subject=%q, dns_sans=%v, ip_sans=%v, key_type=%T",
				parsed.Subject.CommonName, parsed.DNSNames, ipSANs, parsed.PublicKey)
		}

		// Log ClientHello details before the handshake completes
		tlsConfig.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			remote := hello.Conn.RemoteAddr().String()
			log.Printf("MQTT [%s]: TLS ClientHello server_name=%q", remote, hello.ServerName)

			// Flag SNI vs cert mismatch
			sni := hello.ServerName
			if sni != "" {
				sniIP := net.ParseIP(sni)
				certMatchesSNI := false
				if parsed, err := x509.ParseCertificate(tlsCert.Certificate[0]); err == nil {
					if sniIP != nil {
						for _, ip := range parsed.IPAddresses {
							if ip.Equal(sniIP) {
								certMatchesSNI = true
								break
							}
						}
					} else {
						for _, dns := range parsed.DNSNames {
							if dns == sni {
								certMatchesSNI = true
								break
							}
						}
					}
				}
				if !certMatchesSNI {
					log.Printf("MQTT [%s]: WARNING: SNI %q does NOT match any cert SAN — client will likely reject the certificate", remote, sni)
				} else {
					log.Printf("MQTT [%s]: SNI %q matches cert SAN", remote, sni)
				}
			}

			var versions []string
			for _, v := range hello.SupportedVersions {
				versions = append(versions, tlsVersionName(v))
			}
			log.Printf("MQTT [%s]: TLS ClientHello supported_versions=[%s]", remote, strings.Join(versions, ", "))

			var ciphers []string
			for _, c := range hello.CipherSuites {
				ciphers = append(ciphers, tlsCipherSuiteName(c))
			}
			log.Printf("MQTT [%s]: TLS ClientHello cipher_suites=[%s]", remote, strings.Join(ciphers, ", "))

			if len(hello.SupportedProtos) > 0 {
				log.Printf("MQTT [%s]: TLS ClientHello alpn=%v", remote, hello.SupportedProtos)
			}

			var sigAlgs []string
			for _, s := range hello.SignatureSchemes {
				sigAlgs = append(sigAlgs, fmt.Sprintf("%s(0x%04x)", tlsSignatureSchemeName(s), uint16(s)))
			}
			log.Printf("MQTT [%s]: TLS ClientHello signature_algorithms=[%s]", remote, strings.Join(sigAlgs, ", "))

			return nil, nil // use default config
		}

		// Enable SSLKEYLOG if env var is set, for Wireshark packet capture
		if keylogFile := os.Getenv("SSLKEYLOGFILE"); keylogFile != "" {
			w, err := os.OpenFile(keylogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
			if err != nil {
				log.Printf("MQTT: warning: could not open SSLKEYLOGFILE %q: %v", keylogFile, err)
			} else {
				tlsConfig.KeyLogWriter = w
				log.Printf("MQTT: TLS key log enabled -> %s", keylogFile)
			}
		}
	}

	addr := p.IP + ":8883"

	if *debug {
		// Use a raw TCP listener wrapped with TLS so we can inspect the first bytes
		rawListener, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("MQTT: failed to listen on %s: %v", addr, err)
		}
		defer rawListener.Close()

		log.Printf("MQTT: listening on %s (serial=%s, model=%s, debug=true)", addr, p.Serial, p.Model)

		for {
			rawConn, err := rawListener.Accept()
			if err != nil {
				log.Printf("MQTT: accept error: %v", err)
				continue
			}
			remote := rawConn.RemoteAddr().String()

			// Wrap with logging to capture raw TLS handshake bytes
			logged := &logConn{Conn: rawConn, tag: remote}
			tlsConn := tls.Server(logged, tlsConfig)
			go handleMqttConnection(tlsConn, p)
			_ = remote
		}
	}

	listener, err := tls.Listen("tcp", addr, tlsConfig)
	if err != nil {
		log.Fatalf("MQTT: failed to listen on %s: %v", addr, err)
	}
	defer listener.Close()

	log.Printf("MQTT: listening on %s (serial=%s, model=%s)", addr, p.Serial, p.Model)

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
	log.Printf("MQTT [%s]: new TCP connection", remote)

	// Perform TLS handshake explicitly to log errors
	if tlsConn, ok := conn.(*tls.Conn); ok {
		if *debug {
			log.Printf("MQTT [%s]: starting TLS handshake", remote)
		}
		tlsConn.SetDeadline(time.Now().Add(10 * time.Second))
		if err := tlsConn.Handshake(); err != nil {
			log.Printf("MQTT [%s]: TLS handshake failed: %v", remote, err)
			// Try to read any remaining bytes for diagnostics
			if *debug {
				peek := make([]byte, 64)
				tlsConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				n, _ := tlsConn.NetConn().Read(peek)
				if n > 0 {
					log.Printf("MQTT [%s]: raw bytes after handshake failure (%d bytes): %s", remote, n, hex.Dump(peek[:n]))
				} else {
					log.Printf("MQTT [%s]: no additional bytes available after handshake failure (client closed connection)", remote)
				}
			}
			return
		}
		tlsConn.SetDeadline(time.Time{}) // clear deadline
		if *debug {
			state := tlsConn.ConnectionState()
			log.Printf("MQTT [%s]: TLS handshake complete (version=%s, cipher=%s, proto=%q, server_name=%q)",
				remote, tlsVersionName(state.Version), tlsCipherSuiteName(state.CipherSuite),
				state.NegotiatedProtocol, state.ServerName)
			if len(state.PeerCertificates) > 0 {
				log.Printf("MQTT [%s]: client presented %d certificate(s)", remote, len(state.PeerCertificates))
			}
		}
	}

	// Read CONNECT packet
	if *debug {
		log.Printf("MQTT [%s]: waiting for CONNECT packet", remote)
	}
	packetType, payload, err := readMqttPacket(conn)
	if err != nil {
		log.Printf("MQTT [%s]: failed to read CONNECT: %v", remote, err)
		return
	}
	if *debug {
		log.Printf("MQTT [%s]: received packet type=%d, payload_len=%d", remote, packetType, len(payload))
	}
	if packetType != 1 { // CONNECT
		log.Printf("MQTT [%s]: expected CONNECT (type 1), got type %d", remote, packetType)
		return
	}

	// Parse CONNECT
	username, password, clientID, err := parseConnect(payload)
	if err != nil {
		log.Printf("MQTT [%s]: invalid CONNECT: %v", remote, err)
		sendConnack(conn, 4) // bad credentials
		return
	}
	if *debug {
		log.Printf("MQTT [%s]: CONNECT client_id=%q username=%q password_len=%d", remote, clientID, username, len(password))
	}

	if username != "bblp" || password != p.AccessCode {
		log.Printf("MQTT [%s]: auth failed (user=%q, expected_user=%q, password_match=%v)", remote, username, "bblp", password == p.AccessCode)
		sendConnack(conn, 5) // not authorized
		return
	}

	// Send CONNACK (success)
	sendConnack(conn, 0)
	log.Printf("MQTT [%s]: authenticated (client_id=%q)", remote, clientID)

	// Read SUBSCRIBE
	if *debug {
		log.Printf("MQTT [%s]: waiting for SUBSCRIBE packet", remote)
	}
	packetType, payload, err = readMqttPacket(conn)
	if err != nil {
		log.Printf("MQTT [%s]: failed to read SUBSCRIBE: %v", remote, err)
		return
	}
	if *debug {
		log.Printf("MQTT [%s]: received packet type=%d, payload_len=%d", remote, packetType, len(payload))
	}
	if packetType != 8 { // SUBSCRIBE
		log.Printf("MQTT [%s]: expected SUBSCRIBE (type 8), got type %d", remote, packetType)
		return
	}

	packetID, topic, err := parseSubscribe(payload)
	if err != nil {
		log.Printf("MQTT [%s]: invalid SUBSCRIBE: %v", remote, err)
		return
	}

	expectedTopic := fmt.Sprintf("device/%s/report", p.Serial)
	if topic != expectedTopic {
		log.Printf("MQTT [%s]: wrong subscribe topic %q (expected %q), closing", remote, topic, expectedTopic)
		return
	}

	// Send SUBACK
	sendSuback(conn, packetID)
	log.Printf("MQTT [%s]: subscribed to %s (packet_id=%d)", remote, topic, packetID)

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
		headerByte, payload, err := readMqttPacketWithFlags(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("MQTT [%s]: read error: %v", remote, err)
			}
			log.Printf("MQTT [%s]: disconnected", remote)
			return
		}

		packetType := int(headerByte) >> 4
		log.Printf("MQTT [%s]: received packet type=%d flags=0x%02x payload_len=%d", remote, packetType, headerByte&0x0F, len(payload))

		switch packetType {
		case 3: // PUBLISH
			qos := (headerByte >> 1) & 0x03
			handleIncomingPublish(payload, p, conn, &writeMu, remote, qos)
		case 8: // SUBSCRIBE (additional subscriptions after the initial one)
			subPacketID, subTopic, subErr := parseSubscribe(payload)
			if subErr != nil {
				log.Printf("MQTT [%s]: invalid SUBSCRIBE: %v", remote, subErr)
			} else {
				log.Printf("MQTT [%s]: additional subscribe to %s (packet_id=%d)", remote, subTopic, subPacketID)
				sendSuback(conn, subPacketID)
			}
		case 12: // PINGREQ
			log.Printf("MQTT [%s]: PINGREQ -> PINGRESP", remote)
			writeMu.Lock()
			_, err := conn.Write([]byte{0xD0, 0x00}) // PINGRESP
			writeMu.Unlock()
			if err != nil {
				log.Printf("MQTT [%s]: failed to write PINGRESP: %v", remote, err)
				return
			}
		case 14: // DISCONNECT
			log.Printf("MQTT [%s]: client sent DISCONNECT", remote)
			return
		default:
			log.Printf("MQTT [%s]: unhandled packet type %d, payload_len=%d", remote, packetType, len(payload))
		}
	}
}

func handleIncomingPublish(payload []byte, p *Printer, conn net.Conn, writeMu *sync.Mutex, remote string, qos byte) {
	if len(payload) < 2 {
		log.Printf("MQTT [%s]: PUBLISH payload too short (%d bytes)", remote, len(payload))
		return
	}
	topicLen := int(binary.BigEndian.Uint16(payload[:2]))
	if 2+topicLen > len(payload) {
		log.Printf("MQTT [%s]: PUBLISH topic length %d exceeds payload %d", remote, topicLen, len(payload))
		return
	}
	topic := string(payload[2 : 2+topicLen])
	offset := 2 + topicLen

	// QoS 1 and 2 have a 2-byte packet identifier after the topic
	var packetID uint16
	if qos >= 1 {
		if offset+2 > len(payload) {
			log.Printf("MQTT [%s]: PUBLISH QoS %d but no packet ID", remote, qos)
			return
		}
		packetID = binary.BigEndian.Uint16(payload[offset : offset+2])
		offset += 2
		log.Printf("MQTT [%s]: PUBLISH QoS=%d packet_id=%d topic=%q", remote, qos, packetID, topic)
	} else {
		log.Printf("MQTT [%s]: PUBLISH QoS=%d topic=%q", remote, qos, topic)
	}

	msgData := payload[offset:]
	log.Printf("MQTT [%s]: PUBLISH message (%d bytes): %s", remote, len(msgData), string(msgData))

	// Send PUBACK for QoS 1
	if qos == 1 {
		puback := []byte{0x40, 0x02, byte(packetID >> 8), byte(packetID)}
		writeMu.Lock()
		_, err := conn.Write(puback)
		writeMu.Unlock()
		if err != nil {
			log.Printf("MQTT [%s]: failed to send PUBACK: %v", remote, err)
		}
	}

	expectedRequest := fmt.Sprintf("device/%s/request", p.Serial)
	if topic != expectedRequest {
		log.Printf("MQTT [%s]: publish to unexpected topic %q (expected %q)", remote, topic, expectedRequest)
		return
	}

	// Strip trailing null bytes (Bambu firmware sends them)
	msgData = bytes.TrimRight(msgData, "\x00")

	// Parse the JSON command
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(msgData, &msg); err != nil {
		log.Printf("MQTT [%s]: invalid JSON in publish: %v (raw=%q)", remote, err, string(msgData))
		return
	}

	keys := make([]string, 0, len(msg))
	for k := range msg {
		keys = append(keys, k)
	}
	log.Printf("MQTT [%s]: command keys: %v", remote, keys)

	// Handle pushall
	if pushRaw, ok := msg["pushing"]; ok {
		var push map[string]string
		json.Unmarshal(pushRaw, &push)
		if push["command"] == "pushall" {
			log.Printf("MQTT [%s]: pushall requested -> sending status", remote)
			publishStatus(conn, writeMu, p)
		}
		return
	}

	// Handle get_version
	if infoRaw, ok := msg["info"]; ok {
		var info map[string]string
		json.Unmarshal(infoRaw, &info)
		if info["command"] == "get_version" {
			seqID := info["sequence_id"]
			log.Printf("MQTT [%s]: get_version requested (sequence_id=%s) -> sending version", remote, seqID)
			reportTopic := fmt.Sprintf("device/%s/report", p.Serial)
			versionJSON := p.VersionJSON(seqID)
			log.Printf("MQTT [%s]: version response (%d bytes): %s", remote, len(versionJSON), string(versionJSON))
			packet := buildPublishPacket(reportTopic, versionJSON)
			writeMu.Lock()
			_, err := conn.Write(packet)
			writeMu.Unlock()
			if err != nil {
				log.Printf("MQTT [%s]: failed to send version response: %v", remote, err)
			} else {
				log.Printf("MQTT [%s]: version response sent successfully", remote)
			}
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

	log.Printf("MQTT [%s]: unhandled command keys: %v", remote, keys)
}

func publishStatus(conn net.Conn, writeMu *sync.Mutex, p *Printer) {
	topic := fmt.Sprintf("device/%s/report", p.Serial)
	statusJSON := p.StatusJSON()
	if *debug {
		var pretty json.RawMessage
		if json.Unmarshal(statusJSON, &pretty) == nil {
			indented, _ := json.MarshalIndent(pretty, "", "  ")
			log.Printf("MQTT [%s] -> %s:\n%s", p.Serial, topic, indented)
		}
	}
	packet := buildPublishPacket(topic, statusJSON)
	writeMu.Lock()
	_, err := conn.Write(packet)
	writeMu.Unlock()
	if err != nil {
		log.Printf("MQTT [%s]: failed to write status publish: %v", conn.RemoteAddr(), err)
	}
}

// --- MQTT packet reading ---

func readMqttPacket(conn net.Conn) (packetType int, payload []byte, err error) {
	return readMqttPacketFull(conn)
}

// readMqttPacketFull reads an MQTT packet and returns the packet type (top 4 bits),
// flags (lower 4 bits), and the payload.
func readMqttPacketFull(conn net.Conn) (packetType int, payload []byte, err error) {
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

// readMqttPacketWithFlags reads an MQTT packet and returns the full header byte.
func readMqttPacketWithFlags(conn net.Conn) (headerByte byte, payload []byte, err error) {
	conn.SetReadDeadline(time.Now().Add(120 * time.Second))

	header := make([]byte, 1)
	if _, err = io.ReadFull(conn, header); err != nil {
		return 0, nil, err
	}
	headerByte = header[0]

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
	return headerByte, payload, nil
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

func parseConnect(payload []byte) (username, password, clientID string, err error) {
	if len(payload) < 10 {
		return "", "", "", fmt.Errorf("CONNECT payload too short (%d bytes)", len(payload))
	}

	// Skip protocol name (2 bytes len + "MQTT")
	protoLen := int(binary.BigEndian.Uint16(payload[:2]))
	protoName := string(payload[2 : 2+protoLen])
	offset := 2 + protoLen // past protocol name

	if offset+4 > len(payload) {
		return "", "", "", fmt.Errorf("CONNECT payload too short after protocol name %q", protoName)
	}

	// Protocol level (1 byte), connect flags (1 byte), keep alive (2 bytes)
	protoLevel := payload[offset]
	connectFlags := payload[offset+1]
	keepAlive := binary.BigEndian.Uint16(payload[offset+2 : offset+4])
	_ = protoLevel
	_ = keepAlive
	offset += 4 // past level, flags, keep-alive

	hasUsername := connectFlags&0x80 != 0
	hasPassword := connectFlags&0x40 != 0

	// Client ID
	if offset+2 > len(payload) {
		return "", "", "", fmt.Errorf("missing client ID")
	}
	clientIDLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
	offset += 2
	if offset+clientIDLen > len(payload) {
		return "", "", "", fmt.Errorf("client ID truncated")
	}
	clientID = string(payload[offset : offset+clientIDLen])
	offset += clientIDLen

	// Username
	if hasUsername {
		if offset+2 > len(payload) {
			return "", "", clientID, fmt.Errorf("missing username")
		}
		usernameLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if offset+usernameLen > len(payload) {
			return "", "", clientID, fmt.Errorf("username truncated")
		}
		username = string(payload[offset : offset+usernameLen])
		offset += usernameLen
	}

	// Password
	if hasPassword {
		if offset+2 > len(payload) {
			return "", "", clientID, fmt.Errorf("missing password")
		}
		passwordLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if offset+passwordLen > len(payload) {
			return "", "", clientID, fmt.Errorf("password truncated")
		}
		password = string(payload[offset : offset+passwordLen])
	}

	return username, password, clientID, nil
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
	if *debug {
		log.Printf("MQTT [%s]: sending CONNACK return_code=%d", conn.RemoteAddr(), returnCode)
	}
	if _, err := conn.Write([]byte{0x20, 0x02, 0x00, returnCode}); err != nil {
		log.Printf("MQTT [%s]: failed to write CONNACK: %v", conn.RemoteAddr(), err)
	}
}

func sendSuback(conn net.Conn, packetID uint16) {
	// Fixed header: 0x90 (SUBACK), remaining length: 3
	// Variable header: packet ID (2 bytes), return code: 0x00 (QoS 0)
	if *debug {
		log.Printf("MQTT [%s]: sending SUBACK packet_id=%d", conn.RemoteAddr(), packetID)
	}
	if _, err := conn.Write([]byte{0x90, 0x03, byte(packetID >> 8), byte(packetID), 0x00}); err != nil {
		log.Printf("MQTT [%s]: failed to write SUBACK: %v", conn.RemoteAddr(), err)
	}
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

// logConn wraps a net.Conn and logs all reads and writes for TLS handshake debugging.
type logConn struct {
	net.Conn
	tag string
}

func (c *logConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.logTLSRecords("recv", b[:n])
	}
	if err != nil {
		log.Printf("MQTT [%s]: wire recv error: %v", c.tag, err)
	}
	return n, err
}

func (c *logConn) Write(b []byte) (int, error) {
	if len(b) > 0 {
		c.logTLSRecords("send", b)
	}
	n, err := c.Conn.Write(b)
	if err != nil {
		log.Printf("MQTT [%s]: wire send error: %v", c.tag, err)
	}
	return n, err
}

// logTLSRecords parses and logs TLS record headers from raw bytes.
func (c *logConn) logTLSRecords(dir string, data []byte) {
	offset := 0
	for offset+5 <= len(data) {
		contentType := data[offset]
		recordVersion := uint16(data[offset+1])<<8 | uint16(data[offset+2])
		recordLen := int(data[offset+3])<<8 | int(data[offset+4])

		typeName := tlsContentTypeName(contentType)
		verName := tlsVersionName(recordVersion)

		detail := ""
		recordBody := data[offset+5:]
		if len(recordBody) > recordLen {
			recordBody = recordBody[:recordLen]
		}

		// Parse handshake messages
		if contentType == 22 && len(recordBody) >= 4 { // Handshake
			hsType := recordBody[0]
			hsLen := int(recordBody[1])<<16 | int(recordBody[2])<<8 | int(recordBody[3])
			hsBody := recordBody[4:]
			detail = fmt.Sprintf(" handshake=%s(%d) hs_len=%d", tlsHandshakeTypeName(hsType), hsType, hsLen)

			// Parse ClientHello extensions
			if hsType == 1 && len(hsBody) >= 38 { // ClientHello
				off := 2 + 32 // version + random
				if off < len(hsBody) {
					sidLen := int(hsBody[off])
					off += 1 + sidLen
				}
				if off+2 <= len(hsBody) {
					csLen := int(hsBody[off])<<8 | int(hsBody[off+1])
					off += 2 + csLen
				}
				if off+1 <= len(hsBody) {
					compLen := int(hsBody[off])
					off += 1 + compLen
				}
				if off+2 <= len(hsBody) {
					extTotalLen := int(hsBody[off])<<8 | int(hsBody[off+1])
					off += 2
					var clientExts []string
					extEnd := off + extTotalLen
					for off+4 <= len(hsBody) && off < extEnd {
						extType := uint16(hsBody[off])<<8 | uint16(hsBody[off+1])
						extLen := int(hsBody[off+2])<<8 | int(hsBody[off+3])
						extData := hsBody[off+4:]
						if len(extData) > extLen {
							extData = extData[:extLen]
						}
						extInfo := fmt.Sprintf("%s(0x%04x,len=%d)", tlsExtensionName(extType), extType, extLen)

						// Parse supported_groups (0x000A)
						if extType == 0x000A && len(extData) >= 2 {
							groupsLen := int(extData[0])<<8 | int(extData[1])
							var groups []string
							for i := 2; i+1 < 2+groupsLen && i+1 < len(extData); i += 2 {
								gid := uint16(extData[i])<<8 | uint16(extData[i+1])
								name := "unknown"
								switch gid {
								case 0x0017:
									name = "P-256"
								case 0x0018:
									name = "P-384"
								case 0x0019:
									name = "P-521"
								case 0x001D:
									name = "X25519"
								case 0x001E:
									name = "X448"
								}
								groups = append(groups, fmt.Sprintf("%s(0x%04x)", name, gid))
							}
							extInfo += fmt.Sprintf("{%s}", strings.Join(groups, ","))
						}

						clientExts = append(clientExts, extInfo)
						off += 4 + extLen
					}
					detail += fmt.Sprintf(" client_extensions=[%s]", strings.Join(clientExts, ", "))
				}
			}

			// Parse ServerHello to extract selected cipher suite and extensions
			if hsType == 2 && len(hsBody) >= 38 { // ServerHello
				serverVersion := uint16(hsBody[0])<<8 | uint16(hsBody[1])
				// 32 bytes of random
				sessionIDLen := int(hsBody[34])
				off := 35 + sessionIDLen
				if off+3 <= len(hsBody) {
					cipherSuite := uint16(hsBody[off])<<8 | uint16(hsBody[off+1])
					detail += fmt.Sprintf(" selected_version=%s selected_cipher=%s",
						tlsVersionName(serverVersion), tlsCipherSuiteName(cipherSuite))
					off += 2 + 1 // cipher(2) + compression(1)
					// Parse extensions
					if off+2 <= len(hsBody) {
						extTotalLen := int(hsBody[off])<<8 | int(hsBody[off+1])
						off += 2
						var exts []string
						extEnd := off + extTotalLen
						for off+4 <= len(hsBody) && off < extEnd {
							extType := uint16(hsBody[off])<<8 | uint16(hsBody[off+1])
							extLen := int(hsBody[off+2])<<8 | int(hsBody[off+3])
							exts = append(exts, fmt.Sprintf("%s(0x%04x,len=%d)", tlsExtensionName(extType), extType, extLen))
							off += 4 + extLen
						}
						detail += fmt.Sprintf(" extensions=[%s]", strings.Join(exts, ", "))
					}
				}
			}

			// Parse Certificate message to log cert details
			if hsType == 11 && len(hsBody) >= 3 { // Certificate
				certsLen := int(hsBody[0])<<16 | int(hsBody[1])<<8 | int(hsBody[2])
				off := 3
				certIdx := 0
				for off+3 <= len(hsBody) && off < 3+certsLen {
					certLen := int(hsBody[off])<<16 | int(hsBody[off+1])<<8 | int(hsBody[off+2])
					off += 3
					if off+certLen <= len(hsBody) {
						if cert, err := x509.ParseCertificate(hsBody[off : off+certLen]); err == nil {
							var ipSANs []string
							for _, ip := range cert.IPAddresses {
								ipSANs = append(ipSANs, ip.String())
							}
							detail += fmt.Sprintf(" cert[%d]={subject=%q, issuer=%q, dns=%v, ips=%v, serial=%s, sig=%s, key=%T(%d-bit), not_before=%s, not_after=%s}",
								certIdx, cert.Subject, cert.Issuer, cert.DNSNames, ipSANs,
								cert.SerialNumber, cert.SignatureAlgorithm, cert.PublicKey,
								cert.PublicKey.(*rsa.PublicKey).N.BitLen(),
								cert.NotBefore.Format("2006-01-02"), cert.NotAfter.Format("2006-01-02"))
						} else {
							detail += fmt.Sprintf(" cert[%d]={parse_error=%v}", certIdx, err)
						}
					}
					off += certLen
					certIdx++
				}
			}

			// Parse ServerKeyExchange to extract signature algorithm
			if hsType == 12 && len(hsBody) >= 4 { // ServerKeyExchange
				// For ECDHE: curve_type(1) + named_curve(2) + pubkey_len(1) + pubkey + sig_algo(2) + sig
				if hsBody[0] == 3 { // named_curve
					curveID := uint16(hsBody[1])<<8 | uint16(hsBody[2])
					pubKeyLen := int(hsBody[3])
					sigOffset := 4 + pubKeyLen
					if sigOffset+2 <= len(hsBody) {
						sigAlgo := uint16(hsBody[sigOffset])<<8 | uint16(hsBody[sigOffset+1])
						detail += fmt.Sprintf(" curve=0x%04x sig_algorithm=%s(0x%04x)",
							curveID, tlsSignatureSchemeName(tls.SignatureScheme(sigAlgo)), sigAlgo)
					}
				}
			}
		}

		// Parse alert messages
		if contentType == 21 && len(recordBody) >= 2 { // Alert
			alertLevel := recordBody[0]
			alertDesc := recordBody[1]
			levelStr := "warning"
			if alertLevel == 2 {
				levelStr = "fatal"
			}
			detail = fmt.Sprintf(" alert=%s desc=%d(%s)", levelStr, alertDesc, tlsAlertName(alertDesc))
		}

		log.Printf("MQTT [%s]: wire %s: TLS record type=%s version=%s len=%d%s",
			c.tag, dir, typeName, verName, recordLen, detail)

		offset += 5 + recordLen
	}
	if offset < len(data) {
		log.Printf("MQTT [%s]: wire %s: %d trailing bytes (partial record)", c.tag, dir, len(data)-offset)
	}
}

func tlsContentTypeName(t byte) string {
	switch t {
	case 20:
		return "ChangeCipherSpec"
	case 21:
		return "Alert"
	case 22:
		return "Handshake"
	case 23:
		return "ApplicationData"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

func tlsHandshakeTypeName(t byte) string {
	switch t {
	case 1:
		return "ClientHello"
	case 2:
		return "ServerHello"
	case 11:
		return "Certificate"
	case 12:
		return "ServerKeyExchange"
	case 13:
		return "CertificateRequest"
	case 14:
		return "ServerHelloDone"
	case 15:
		return "CertificateVerify"
	case 16:
		return "ClientKeyExchange"
	case 20:
		return "Finished"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

func tlsAlertName(desc byte) string {
	switch desc {
	case 0:
		return "close_notify"
	case 10:
		return "unexpected_message"
	case 20:
		return "bad_record_mac"
	case 40:
		return "handshake_failure"
	case 42:
		return "bad_certificate"
	case 43:
		return "unsupported_certificate"
	case 44:
		return "certificate_revoked"
	case 45:
		return "certificate_expired"
	case 46:
		return "certificate_unknown"
	case 47:
		return "illegal_parameter"
	case 48:
		return "unknown_ca"
	case 49:
		return "access_denied"
	case 50:
		return "decode_error"
	case 51:
		return "decrypt_error"
	case 70:
		return "protocol_version"
	case 71:
		return "insufficient_security"
	case 80:
		return "internal_error"
	case 86:
		return "inappropriate_fallback"
	case 90:
		return "user_canceled"
	case 100:
		return "no_renegotiation"
	case 109:
		return "missing_extension"
	case 110:
		return "unsupported_extension"
	case 112:
		return "unrecognized_name"
	default:
		return fmt.Sprintf("unknown(%d)", desc)
	}
}

// --- TLS certificate generation ---

// CA holds a generated certificate authority used to sign printer certificates.
type CA struct {
	Key     *rsa.PrivateKey
	Cert    *x509.Certificate
	CertDER []byte
}

// LoadOrGenerateCA loads an existing CA from disk, or generates a new one and
// saves it. Returns the CA and whether a new one was created.
func LoadOrGenerateCA(certFile, keyFile string) (*CA, bool) {
	certPEM, certErr := os.ReadFile(certFile)
	keyPEM, keyErr := os.ReadFile(keyFile)
	if certErr == nil && keyErr == nil {
		certBlock, _ := pem.Decode(certPEM)
		keyBlock, _ := pem.Decode(keyPEM)
		if certBlock != nil && keyBlock != nil {
			cert, err := x509.ParseCertificate(certBlock.Bytes)
			if err == nil {
				key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
				if err == nil {
					return &CA{Key: key, Cert: cert, CertDER: certBlock.Bytes}, false
				}
			}
		}
	}

	ca := GenerateCA()
	certOut := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.CertDER})
	keyOut := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(ca.Key)})
	if err := os.WriteFile(certFile, certOut, 0644); err != nil {
		log.Fatalf("Failed to write CA certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, keyOut, 0600); err != nil {
		log.Fatalf("Failed to write CA key: %v", err)
	}
	return ca, true
}

// GenerateCA creates a new certificate authority for signing printer certificates.
func GenerateCA() *CA {
	now := time.Now().UTC()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("Failed to generate CA key: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Virtual Printer CA"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(20 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		log.Fatalf("Failed to create CA certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		log.Fatalf("Failed to parse CA certificate: %v", err)
	}

	return &CA{Key: caKey, Cert: caCert, CertDER: caCertDER}
}

// generateCertChain creates a printer certificate signed by the given CA,
// mimicking the real Bambu Lab printer certificate structure.
func generateCertChain(ca *CA, serial, ip string) tls.Certificate {
	now := time.Now().UTC()

	printerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("Failed to generate printer key: %v", err)
	}

	printerTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: serial},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  false,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:           []net.IP{net.ParseIP(ip), net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost", serial},
	}

	printerCertDER, err := x509.CreateCertificate(rand.Reader, printerTemplate, ca.Cert, &printerKey.PublicKey, ca.Key)
	if err != nil {
		log.Fatalf("Failed to create printer certificate: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{printerCertDER, ca.CertDER},
		PrivateKey:  printerKey,
	}
}
