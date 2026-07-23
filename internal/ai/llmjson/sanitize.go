package llmjson

import "strings"

func SanitizeUTF8(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x80 {
			buf.WriteByte(b)
		} else if b < 0xC0 {
			buf.WriteRune('\uFFFD')
		} else if b < 0xE0 {
			if i+1 < len(s) && (s[i+1]&0xC0) == 0x80 {
				buf.WriteByte(b)
				buf.WriteByte(s[i+1])
				i++
			} else {
				buf.WriteRune('\uFFFD')
			}
		} else if b < 0xF0 {
			if i+2 < len(s) && (s[i+1]&0xC0) == 0x80 && (s[i+2]&0xC0) == 0x80 {
				buf.WriteByte(b)
				buf.WriteByte(s[i+1])
				buf.WriteByte(s[i+2])
				i += 2
			} else {
				buf.WriteRune('\uFFFD')
			}
		} else if b < 0xF8 {
			if i+3 < len(s) && (s[i+1]&0xC0) == 0x80 && (s[i+2]&0xC0) == 0x80 && (s[i+3]&0xC0) == 0x80 {
				buf.WriteByte(b)
				buf.WriteByte(s[i+1])
				buf.WriteByte(s[i+2])
				buf.WriteByte(s[i+3])
				i += 3
			} else {
				buf.WriteRune('\uFFFD')
			}
		} else {
			buf.WriteRune('\uFFFD')
		}
	}
	return buf.String()
}
