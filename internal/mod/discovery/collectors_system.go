package discovery

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// SystemCollector collects system-level configuration and account data.
type SystemCollector struct{}

func (c *SystemCollector) Name() string           { return "system" }
func (c *SystemCollector) Timeout() time.Duration { return 25 * time.Second }

func (c *SystemCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	result := &systemCollectorResult{}

	if out, err := execText(ctx, exec, `free -m | awk '/^Swap:/ {print $2, $3, $4}'`); err == nil {
		if swap, perr := parseSwap(out); perr == nil {
			result.Swap = swap
		} else {
			result.addError("swap", perr)
		}
	} else {
		result.addError("swap", err)
	}

	if out, err := execText(ctx, exec, `findmnt -lno SOURCE,TARGET,FSTYPE,SIZE,USED,AVAIL,USE%,OPTIONS 2>/dev/null`); err == nil {
		if filesystems, perr := parseFilesystems(out); perr == nil {
			result.Filesystems = filesystems
		} else {
			result.addError("filesystems", perr)
		}
	} else {
		result.addError("filesystems", err)
	}

	if out, err := execText(ctx, exec, `lsblk -b -o NAME,TYPE,SIZE,MOUNTPOINT,FSTYPE,MODEL,RO 2>/dev/null`); err == nil {
		if devices, perr := parseBlockDevices(out); perr == nil {
			result.BlockDevices = devices
		} else {
			result.addError("block_devices", perr)
		}
	} else {
		result.addError("block_devices", err)
	}

	if out, err := execText(ctx, exec, `locale 2>/dev/null | grep -E '^(LANG|LC_)'`); err == nil {
		result.Locale = parseLocale(out)
	} else {
		result.addError("locale", err)
	}

	passwdOut, passwdErr := execText(ctx, exec, `cat /etc/passwd 2>/dev/null`)
	groupOut, groupErr := execText(ctx, exec, `cat /etc/group 2>/dev/null`)
	if passwdErr == nil {
		users, perr := parseUsers(passwdOut)
		if perr == nil {
			if groupErr == nil {
				groupsByName, membersByUser, gerr := parseGroupMembership(groupOut)
				if gerr == nil {
					for i := range users {
						if members, ok := membersByUser[users[i].Username]; ok {
							users[i].Groups = append(users[i].Groups, members...)
						}
					}
					_ = groupsByName
				} else {
					result.addError("groups", gerr)
				}
			}
			result.Users = users
		} else {
			result.addError("users", perr)
		}
	} else {
		result.addError("users", passwdErr)
	}
	if groupErr == nil {
		groups, perr := parseGroups(groupOut)
		if perr == nil {
			result.Groups = groups
		} else {
			result.addError("groups", perr)
		}
	} else {
		result.addError("groups", groupErr)
	}

	if out, err := execText(ctx, exec, `cat /etc/ssh/sshd_config 2>/dev/null`); err == nil {
		sshCfg, perr := parseSSHConfig(out)
		if perr == nil {
			if keys, kerr := discoverAuthorizedKeys(ctx, exec, sshCfg); kerr == nil {
				sshCfg.AuthorizedKeys = keys
			} else {
				result.addError("ssh_config", kerr)
			}
			result.SSHConfig = sshCfg
		} else {
			result.addError("ssh_config", perr)
		}
	} else {
		result.addError("ssh_config", err)
	}

	if out, err := execText(ctx, exec, `cat /etc/resolv.conf 2>/dev/null`); err == nil {
		if dns, perr := parseDNS(out); perr == nil {
			result.DNS = dns
		} else {
			result.addError("dns", perr)
		}
	} else {
		result.addError("dns", err)
	}

	return result, nil
}

type systemCollectorResult struct {
	Swap         *SwapInfo
	Filesystems  []FilesystemInfo
	BlockDevices []BlockDeviceInfo
	Locale       string
	Users        []UserInfo
	Groups       []GroupInfo
	SSHConfig    *SSHConfigInfo
	DNS          *DNSInfo
	errors       []CollectorError
}

func (r *systemCollectorResult) addError(collector string, err error) {
	if err == nil {
		return
	}
	r.errors = append(r.errors, CollectorError{Collector: collector, Error: err.Error()})
}

func (r *systemCollectorResult) collectorErrors() []CollectorError {
	return append([]CollectorError(nil), r.errors...)
}

func execText(ctx context.Context, exec transport.SSHExecuter, cmd string) (string, error) {
	if exec == nil {
		return "", fmt.Errorf("ssh executer is nil")
	}
	stdout, _, _, err := exec.ExecContext(ctx, cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

func parseSwap(out string) (*SwapInfo, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return &SwapInfo{}, nil
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "total") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "swap:") {
			fields := strings.Fields(line)
			if len(fields) < 4 {
				return nil, fmt.Errorf("unexpected swap output: %q", out)
			}
			total, err := strconv.Atoi(fields[1])
			if err != nil {
				return nil, err
			}
			used, err := strconv.Atoi(fields[2])
			if err != nil {
				return nil, err
			}
			free, err := strconv.Atoi(fields[3])
			if err != nil {
				return nil, err
			}
			return &SwapInfo{TotalMB: total, UsedMB: used, FreeMB: free}, nil
		}
	}

	fields := strings.Fields(out)
	if len(fields) < 3 {
		return nil, fmt.Errorf("unexpected swap output: %q", out)
	}
	total, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, err
	}
	used, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, err
	}
	free, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, err
	}
	return &SwapInfo{TotalMB: total, UsedMB: used, FreeMB: free}, nil
}

func parseFilesystems(out string) ([]FilesystemInfo, error) {
	var result []FilesystemInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		total, _ := parseSizeToGB(fields[3])
		used, _ := parseSizeToGB(fields[4])
		avail, _ := parseSizeToGB(fields[5])
		usePct, _ := strconv.ParseFloat(strings.TrimSuffix(fields[6], "%"), 64)
		result = append(result, FilesystemInfo{
			Device:       fields[0],
			MountPoint:   fields[1],
			FSType:       fields[2],
			TotalGB:      total,
			UsedGB:       used,
			AvailGB:      avail,
			UsePercent:   usePct,
			MountOptions: fields[7],
		})
	}
	return result, nil
}

func parseBlockDevices(out string) ([]BlockDeviceInfo, error) {
	var result []BlockDeviceInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToUpper(line), "NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		sizeBytes, _ := strconv.ParseFloat(fields[2], 64)
		result = append(result, BlockDeviceInfo{
			Name:       fields[0],
			Type:       fields[1],
			SizeGB:     sizeBytes / (1024 * 1024 * 1024),
			MountPoint: fields[3],
			FSType:     fields[4],
			Model:      fields[5],
			ReadOnly:   fields[6] == "1",
		})
	}
	return result, nil
}

func parseLocale(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "LANG=") {
			return strings.TrimPrefix(line, "LANG=")
		}
	}
	return ""
}

func parseUsers(out string) ([]UserInfo, error) {
	var users []UserInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		if uid != 0 && uid < 1000 {
			continue
		}
		gid, _ := strconv.Atoi(fields[3])
		users = append(users, UserInfo{
			Username: fields[0],
			UID:      uid,
			GID:      gid,
			HomeDir:  fields[5],
			Shell:    fields[6],
		})
	}
	return users, nil
}

func parseGroups(out string) ([]GroupInfo, error) {
	var groups []GroupInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 3 {
			continue
		}
		gid, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		groups = append(groups, GroupInfo{Name: fields[0], GID: gid})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].GID < groups[j].GID })
	return groups, nil
}

func parseGroupMembership(out string) (map[string]GroupInfo, map[string][]string, error) {
	groupsByName := make(map[string]GroupInfo)
	membersByUser := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 4 {
			continue
		}
		gid, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		group := GroupInfo{Name: fields[0], GID: gid}
		groupsByName[group.Name] = group
		for _, member := range strings.Split(fields[3], ",") {
			member = strings.TrimSpace(member)
			if member == "" {
				continue
			}
			membersByUser[member] = append(membersByUser[member], group.Name)
		}
	}
	return groupsByName, membersByUser, nil
}

func parseSSHConfig(out string) (*SSHConfigInfo, error) {
	cfg := &SSHConfigInfo{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		value := strings.Join(fields[1:], " ")
		switch key {
		case "port":
			if port, err := strconv.Atoi(value); err == nil {
				cfg.Port = port
			}
		case "permitrootlogin":
			cfg.PermitRootLogin = value
		case "passwordauthentication":
			cfg.PasswordAuth = value
		case "pubkeyauthentication":
			cfg.PubkeyAuth = value
		case "authorizedkeysfile":
			cfg.AuthorizedKeys = append(cfg.AuthorizedKeys, strings.Fields(value)...)
		case "hostkey":
			cfg.HostKeys = append(cfg.HostKeys, value)
		}
	}
	return cfg, nil
}

func discoverAuthorizedKeys(ctx context.Context, exec transport.SSHExecuter, cfg *SSHConfigInfo) ([]string, error) {
	if cfg == nil {
		return nil, nil
	}
	var paths []string
	for _, cmd := range []string{
		`find /root/.ssh /home -path '*/.ssh/authorized_keys' -type f 2>/dev/null`,
		`find /home -path '*/.ssh/authorized_keys' -type f 2>/dev/null`,
	} {
		out, err := execText(ctx, exec, cmd)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				paths = append(paths, line)
			}
		}
	}
	paths = append(paths, cfg.AuthorizedKeys...)
	return uniqueStrings(paths), nil
}

func parseDNS(out string) (*DNSInfo, error) {
	dns := &DNSInfo{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			dns.Nameservers = append(dns.Nameservers, fields[1])
		case "search":
			if dns.SearchDomain == "" {
				dns.SearchDomain = strings.Join(fields[1:], " ")
			}
		}
	}
	return dns, nil
}

func parseSizeToGB(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "B")
	multiplier := 1.0
	switch {
	case strings.HasSuffix(s, "K"):
		multiplier = 1.0 / (1024 * 1024)
		s = strings.TrimSuffix(s, "K")
	case strings.HasSuffix(s, "M"):
		multiplier = 1.0 / 1024
		s = strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "G"):
		multiplier = 1
		s = strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "T"):
		multiplier = 1024
		s = strings.TrimSuffix(s, "T")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, err
	}
	return v * multiplier, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func shellQuotePath(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}
