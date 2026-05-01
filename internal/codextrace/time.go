package codextrace

import (
	"fmt"
	"time"
)

func ISOToNS(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "0"
	}
	return fmt.Sprintf("%d", parsed.UnixNano())
}

func ObservationBounds(timestamp string, duration any) (string, string) {
	end := int64Value(ISOToNS(timestamp))
	elapsed := durationToNS(duration)
	start := end - elapsed
	if start < 0 {
		start = end
	}
	return fmt.Sprintf("%d", start), fmt.Sprintf("%d", end)
}

func durationToNS(value any) int64 {
	duration := mapValue(value)
	if len(duration) == 0 {
		return 0
	}
	return int64(intValue(duration["secs"]))*1_000_000_000 + int64(intValue(duration["nanos"]))
}

func int64Value(value string) int64 {
	var result int64
	_, _ = fmt.Sscanf(value, "%d", &result)
	return result
}
