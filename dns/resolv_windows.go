//go:build windows

package dns

func systemNamerservers() string {
	cmd := exec.Command("ipconfig", "/all")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// Parse output to find DNS servers
	// This is a simplified and not robust example
	var servers []string
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "DNS Servers") {
			// Extract and append the DNS server
		}
	}
	return servers, nil
}
