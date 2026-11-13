package auth

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

var validSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"Công ty ABC", "cong-ty-abc"},
		{"Công ty TNHH Đại Phát", "cong-ty-tnhh-dai-phat"},
		{"  Acme, Inc.  ", "acme-inc"},
		{"Phở 24/7", "pho-24-7"},
		{"Ánh Dương -- Nguyễn", "anh-duong-nguyen"},
		{"日本株式会社", fallbackSlug},
		{"AB", fallbackSlug},
		{"", fallbackSlug},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugify(tt.name)
			assert.Equal(t, tt.want, got)
			assert.Regexp(t, validSlug, got)
		})
	}
}

func TestSlugify_LongNameLeavesRoomForSuffix(t *testing.T) {
	got := slugify(strings.Repeat("Công ty rất dài ", 10))

	assert.LessOrEqual(t, len(got), maxSlugLen-1-slugSuffixLen)
	assert.Regexp(t, validSlug, got)
	assert.LessOrEqual(t, len(withRandomSuffix(got)), maxSlugLen)
	assert.Regexp(t, validSlug, withRandomSuffix(got))
}
