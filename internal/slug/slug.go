package slug

import "strings"

// DashedLower preserves lowercase ASCII alphanumerics, replaces every other
// byte with a dash, and trims leading and trailing dashes.
func DashedLower(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for _, char := range value {
		switch {
		case char >= 'A' && char <= 'Z':
			result.WriteRune(char + ('a' - 'A'))
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			result.WriteRune(char)
		default:
			result.WriteByte('-')
		}
	}
	return strings.Trim(result.String(), "-")
}

// Dashed preserves ASCII alphanumerics and replaces every other byte with a
// dash. It deliberately does not collapse or trim dashes.
func Dashed(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for _, char := range value {
		if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			result.WriteRune(char)
		} else {
			result.WriteByte('-')
		}
	}
	return result.String()
}
