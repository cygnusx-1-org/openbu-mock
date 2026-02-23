package main

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// getLocalIPAndInterface returns the local IP and the network interface it belongs to.
func getLocalIPAndInterface() (string, string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", "", fmt.Errorf("failed to detect local IP: %w", err)
	}
	defer conn.Close()

	localIP := conn.LocalAddr().(*net.UDPAddr).IP.String()

	ifaces, err := net.Interfaces()
	if err != nil {
		return localIP, "", fmt.Errorf("failed to list interfaces: %w", err)
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipnet.IP.String() == localIP {
				return localIP, iface.Name, nil
			}
		}
	}

	return localIP, "", fmt.Errorf("could not find interface for IP %s", localIP)
}

// incrementIP adds an offset to the last octet of an IPv4 address.
// Returns an error if the result would leave the /24 subnet (last octet 1-254).
func incrementIP(baseIP string, offset int) (string, error) {
	ip := net.ParseIP(baseIP).To4()
	if ip == nil {
		return "", fmt.Errorf("invalid IPv4 address: %s", baseIP)
	}

	lastOctet := int(ip[3]) + offset
	if lastOctet < 1 || lastOctet > 254 {
		return "", fmt.Errorf("offset %+d from %s yields octet %d (out of range 1-254)", offset, baseIP, lastOctet)
	}

	return fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], lastOctet), nil
}

// findAvailableIPs finds N available IPs near baseIP using a zigzag pattern:
// +1, -1, +2, -2, +3, -3, ... skipping IPs that are already in use or already claimed.
func findAvailableIPs(baseIP string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}

	claimed := make(map[string]bool)
	var results []string

	for step := 1; len(results) < n; step++ {
		// Try both +step and -step
		for _, offset := range []int{step, -step} {
			if len(results) >= n {
				break
			}

			candidate, err := incrementIP(baseIP, offset)
			if err != nil {
				continue // out of subnet range, skip
			}

			if claimed[candidate] {
				continue
			}

			if !isIPAvailable(candidate) {
				claimed[candidate] = true
				continue
			}

			results = append(results, candidate)
			claimed[candidate] = true
		}

		// Safety: don't search forever (a /24 has at most 253 usable addresses)
		if step > 253 {
			return results, fmt.Errorf("could only find %d of %d available IPs", len(results), n)
		}
	}

	return results, nil
}

// isIPAvailable checks if an IP address is not already in use on the network.
func isIPAvailable(ip string) bool {
	// Try arping first (more reliable for LAN)
	cmd := exec.Command("arping", "-c", "1", "-w", "1", ip)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// arping failed or not found — treat as available
		return true
	}
	// If arping got a reply, the IP is in use
	return !strings.Contains(string(output), "reply from")
}

// addIPAlias adds an IP alias to a network interface using sudo.
func addIPAlias(iface, ip string) error {
	cmd := exec.Command("sudo", "ip", "addr", "add", ip+"/24", "dev", iface)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to add IP alias %s on %s: %s (%w)", ip, iface, strings.TrimSpace(string(output)), err)
	}
	return nil
}

// removeIPAlias removes an IP alias from a network interface using sudo.
func removeIPAlias(iface, ip string) error {
	cmd := exec.Command("sudo", "ip", "addr", "del", ip+"/24", "dev", iface)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to remove IP alias %s from %s: %s (%w)", ip, iface, strings.TrimSpace(string(output)), err)
	}
	return nil
}

// cleanupAliases removes all added IP aliases.
func cleanupAliases(iface string, ips []string) {
	for _, ip := range ips {
		if err := removeIPAlias(iface, ip); err != nil {
			fmt.Printf("Warning: %v\n", err)
		} else {
			fmt.Printf("Removed IP alias %s from %s\n", ip, iface)
		}
	}
}
