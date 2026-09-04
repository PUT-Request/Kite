package tool

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type TimezoneCurrent struct{}

func (t *TimezoneCurrent) Name() string        { return "timezone_current" }
func (t *TimezoneCurrent) Description() string { return "Get the current local time and offset for a timezone. Use IANA names like 'America/New_York', 'Asia/Shanghai', 'Europe/London'. Returns time in a readable format." }
func (t *TimezoneCurrent) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"zone": strParam("IANA timezone name (e.g., 'America/New_York', 'Asia/Tokyo', 'UTC')."),
	}, []string{"zone"})
}

func (t *TimezoneCurrent) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	zone := getStringArg(args, "zone")
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return fmt.Sprintf("Unknown timezone '%s'. Try 'America/New_York', 'Europe/London', 'Asia/Shanghai', etc.", zone), nil
	}
	now := time.Now().In(loc)
	utcOff := now.Format("-0700")
	name, _ := now.Zone()
	return fmt.Sprintf("%s — %s (UTC%s, %s)", zone, now.Format("Mon Jan 2 2006, 3:04 PM"), utcOff, name), nil
}

type TimezoneConvert struct{}

func (t *TimezoneConvert) Name() string        { return "timezone_convert" }
func (t *TimezoneConvert) Description() string { return "Convert a time between two timezones. Provide a time string and source/dest timezones. Time can be natural like '3pm tomorrow' or a specific timestamp." }
func (t *TimezoneConvert) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"time_str":  strParam("Time to convert. Can be 'now', '3pm', '2026-06-23 14:00', or 'tomorrow at noon'."),
		"from_zone": strParam("Source IANA timezone (e.g., 'America/New_York')."),
		"to_zone":   strParam("Target IANA timezone (e.g., 'Asia/Shanghai')."),
	}, []string{"time_str", "from_zone", "to_zone"})
}

func (t *TimezoneConvert) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	timeStr := getStringArg(args, "time_str")
	fromZone := getStringArg(args, "from_zone")
	toZone := getStringArg(args, "to_zone")
	if timeStr == "" || fromZone == "" || toZone == "" {
		return "Error: time_str, from_zone, and to_zone are required", nil
	}

	fromLoc, err := time.LoadLocation(fromZone)
	if err != nil {
		return fmt.Sprintf("Unknown timezone '%s'", fromZone), nil
	}
	toLoc, err := time.LoadLocation(toZone)
	if err != nil {
		return fmt.Sprintf("Unknown timezone '%s'", toZone), nil
	}

	var moment time.Time
	lower := strings.ToLower(strings.TrimSpace(timeStr))
	now := time.Now()
	switch {
	case lower == "now":
		moment = now.In(fromLoc)
	case lower == "tomorrow":
		moment = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, fromLoc)
	default:
		formats := []string{
			"2006-01-02 15:04",
			"2006-01-02 3:04pm",
			"2006-01-02 3:04 PM",
			"2006-01-02",
			"3pm",
			"3 pm",
			"3:04pm",
			"3:04 PM",
			"15:04",
		}
		parsed := false
		for _, f := range formats {
			if t, e := time.ParseInLocation(f, timeStr, fromLoc); e == nil {
				moment = t
				parsed = true
				break
			}
		}
		if !parsed {
			return fmt.Sprintf("Could not parse time '%s'. Use formats like '3pm', '2026-06-23 14:00', or 'now'.", timeStr), nil
		}
	}
	if moment.Year() < 2000 {
		moment = time.Date(now.Year(), now.Month(), now.Day(), moment.Hour(), moment.Minute(), 0, 0, fromLoc)
	}

	converted := moment.In(toLoc)
	return fmt.Sprintf("%s in %s = %s in %s",
		moment.Format("Mon Jan 2 2006, 3:04 PM MST"),
		fromZone,
		converted.Format("Mon Jan 2 2006, 3:04 PM MST"),
		toZone,
	), nil
}

type TimezoneList struct{}

func (t *TimezoneList) Name() string        { return "timezone_list" }
func (t *TimezoneList) Description() string { return "List common IANA timezone names for reference. Use to look up valid timezone IDs." }
func (t *TimezoneList) Parameters() map[string]interface{} {
	return params(map[string]interface{}{}, nil)
}

func (t *TimezoneList) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	zones := []string{
		"UTC",
		"America/New_York (US Eastern)",
		"America/Chicago (US Central)",
		"America/Denver (US Mountain)",
		"America/Los_Angeles (US Pacific)",
		"America/Toronto (Eastern Canada)",
		"America/Vancouver (Pacific Canada)",
		"America/Mexico_City",
		"America/Sao_Paulo",
		"America/Argentina/Buenos_Aires",
		"Europe/London (UK)",
		"Europe/Paris (Central Europe)",
		"Europe/Berlin (Central Europe)",
		"Europe/Moscow",
		"Europe/Istanbul",
		"Asia/Dubai",
		"Asia/Kolkata (India)",
		"Asia/Shanghai (China)",
		"Asia/Tokyo (Japan)",
		"Asia/Seoul (Korea)",
		"Asia/Singapore",
		"Asia/Bangkok",
		"Asia/Jakarta",
		"Asia/Manila",
		"Australia/Sydney",
		"Australia/Perth",
		"Pacific/Auckland (New Zealand)",
		"Pacific/Honolulu (Hawaii)",
	}
	return strings.Join(zones, "\n"), nil
}
