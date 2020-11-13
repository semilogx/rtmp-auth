package main

import (
	"regexp"
	"strconv"
	"time"
	"net/http"
)

type handleFunc func(http.ResponseWriter, *http.Request)

var durationRegex = regexp.MustCompile(`P([\d\.]+Y)?([\d\.]+M)?([\d\.]+D)?T?([\d\.]+H)?([\d\.]+M)?([\d\.]+?S)?`)

func parseDurationPart(value string, unit time.Duration) time.Duration {
	if len(value) != 0 {
		if parsed, err := strconv.ParseFloat(value[:len(value)-1], 64); err == nil {
			return time.Duration(float64(unit) * parsed)
		}
	}
	return 0
}

// Parse expiration time
func ParseExpiry(str string) *int64 {
	// Allow empty string for "never"
	if str == "" {
		never := int64(-1)
		return &never
	}

	// Try to parse as ISO8601 duration
	matches := durationRegex.FindStringSubmatch(str)
	if matches != nil {
		years := parseDurationPart(matches[1], time.Hour*24*365)
		months := parseDurationPart(matches[2], time.Hour*24*30)
		days := parseDurationPart(matches[3], time.Hour*24)
		hours := parseDurationPart(matches[4], time.Hour)
		minutes := parseDurationPart(matches[5], time.Second*60)
		seconds := parseDurationPart(matches[6], time.Second)
		d := time.Duration(years + months + days + hours + minutes + seconds)
		if d == 0 {
			return nil
		}

		expiry := time.Now().Add(d).Unix()
		return &expiry
	}

	// Try to parse as absolute time
	t, err := time.Parse(time.RFC3339, str)
	if err != nil {
		return nil
	}
	expiry := t.Unix()
	return &expiry
}
