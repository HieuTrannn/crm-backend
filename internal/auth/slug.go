package auth

import (
	"crypto/rand"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	maxSlugLen = 50
	// slugSuffixLen random characters are appended when the plain slug is taken.
	slugSuffixLen = 6
	// fallbackSlug is used when a company name has no Latin letters or digits.
	fallbackSlug = "tenant"
)

// slugify turns a company name into a URL-safe slug: "Công ty Đại Phát" -> "cong-ty-dai-phat".
// It strips Vietnamese (and other Latin) diacritics, lowercases, and joins runs
// of anything else with single hyphens. The result leaves room for a suffix.
func slugify(name string) string {
	var b strings.Builder
	hyphen := false
	// NFD splits "ộ" into "o" + combining marks, which are then dropped.
	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case r == 'đ':
			r = 'd'
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			hyphen = false
		} else {
			hyphen = true
		}
	}

	slug := b.String()
	if limit := maxSlugLen - 1 - slugSuffixLen; len(slug) > limit {
		slug = strings.TrimRight(slug[:limit], "-")
	}
	if len(slug) < 3 {
		return fallbackSlug
	}
	return slug
}

// withRandomSuffix returns slug plus "-" and 6 random lowercase base32 characters.
func withRandomSuffix(slug string) string {
	return slug + "-" + strings.ToLower(rand.Text()[:slugSuffixLen])
}
