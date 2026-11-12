package color

import "fmt"

const (
	Red    = "\033[31m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Reset  = "\033[0m"
)

// Errorf formats a message wrapped in red.
func Errorf(format string, args ...any) string {
	return Red + fmt.Sprintf(format, args...) + Reset
}

// Warnf formats a message wrapped in yellow.
func Warnf(format string, args ...any) string {
	return Yellow + fmt.Sprintf(format, args...) + Reset
}

// Cmdf formats a message wrapped in blue.
func Cmdf(format string, args ...any) string {
	return Blue + fmt.Sprintf(format, args...) + Reset
}
