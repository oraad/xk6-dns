package dns

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Nameserver represents a DNS nameserver.
type Nameserver struct {
	// IPAddr is the IP address of the nameserver.
	IP net.IP

	// Port is the port of the nameserver.
	Port uint16
}

// Addr returns the address of the nameserver as a string.
func (n Nameserver) Addr() string {
	return n.IP.String() + ":" + strconv.Itoa(int(n.Port))
}

// ParseNameserverAddr parses a nameserver address string into an IP and a port.
//
// It expects the `addr` to be in the format `ip` or `ip[:port]`. Where `ip` can be an IPv4 or an IPv6 address.
func parseNameserverAddr(addr string) (Nameserver, error) {
	hostStr, portStr, err := parseHostAndPort(addr)
	if err != nil {
		return Nameserver{}, err
	}

	// Parse the host part into an IP
	ip := net.ParseIP(hostStr)
	if ip == nil {
		return Nameserver{}, fmt.Errorf("invalid nameserver IP address: %s", hostStr)
	}

	if portStr == "" {
		portStr = "53"
	}

	// Convert the port to an integer
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Nameserver{}, fmt.Errorf("nameserver port is not a number: %w", err)
	}

	if port < 0 || port > 65535 {
		return Nameserver{}, fmt.Errorf("nameserver port out of range: %d", port)
	}

	return Nameserver{ip, uint16(port)}, nil
}

func parseHostAndPort(addr string) (string, string, error) {
	var err error
	var host string
	port := "53"

	// Check if the address contains a port
	if strings.ContainsRune(addr, ':') {
		// Split the host and the port from the address string
		host, port, err = net.SplitHostPort(addr)
		if err == nil {
			return host, port, nil
		}

		// IPv6 addresses can contain colons, so we need to check if the error is due to an IPv6 address
		// without a port, or if it's an actual error.
		if strings.Contains(addr, "]") {
			// Try to trim the brackets from the IPv6 address and parse it without the port
			if addr[0] == '[' && addr[len(addr)-1] == ']' {
				host = addr[1 : len(addr)-1]
				return host, port, nil
			}
		}

		return "", "", fmt.Errorf("invalid nameserver address format: %w", err)
	}

	host = addr

	return host, port, nil
}
