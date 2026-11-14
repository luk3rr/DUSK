package ddc

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseDetect parses the output of `ddcutil detect --brief`.
// "Invalid display" blocks and blocks without a Monitor: line are ignored.
func ParseDetect(out string) []Info {
	var res []Info
	var cur *Info
	valid := false
	flush := func() {
		if cur != nil && valid && cur.Model != "" && cur.Bus >= 0 {
			res = append(res, *cur)
		}
		cur, valid = nil, false
	}
	for _, line := range trimLines(out) {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "Display "):
			flush()
			cur = &Info{Bus: -1}
			valid = true
		case strings.HasPrefix(trim, "Invalid display"):
			flush()
			cur = &Info{Bus: -1}
			valid = false
		case cur == nil:
		default:
			key, val, ok := strings.Cut(trim, ":")
			if !ok {
				continue
			}
			val = strings.TrimSpace(val)
			switch strings.TrimSpace(key) {
			case "I2C bus":
				if n, err := strconv.Atoi(strings.TrimPrefix(val, "/dev/i2c-")); err == nil {
					cur.Bus = n
				}
			case "DRM connector":
				cur.Connector = val
			case "Monitor":
				parts := strings.SplitN(val, ":", 3)
				if len(parts) < 2 {
					continue
				}
				cur.Mfg, cur.Model = parts[0], parts[1]
				if len(parts) == 3 {
					cur.Serial = parts[2]
				}
			}
		}
	}
	flush()
	return res
}

// parseGetVCP parses `VCP <code> C <current> <max>` (getvcp --brief).
func parseGetVCP(out string) (cur, max int, err error) {
	for _, line := range trimLines(out) {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "VCP" {
			continue
		}
		if len(f) != 5 || f[2] != "C" {
			return 0, 0, fmt.Errorf("unexpected response: %q", strings.TrimSpace(line))
		}
		c, err1 := strconv.Atoi(f[3])
		m, err2 := strconv.Atoi(f[4])
		if err1 != nil || err2 != nil {
			return 0, 0, fmt.Errorf("invalid values: %q", strings.TrimSpace(line))
		}
		return c, m, nil
	}
	return 0, 0, fmt.Errorf("no VCP line in response: %q", strings.TrimSpace(out))
}
