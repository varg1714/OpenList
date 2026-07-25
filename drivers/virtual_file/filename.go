package virtual_file

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var realNameRegexp = regexp.MustCompile("(.+?)(?:-cd\\d+)?(?:-background)?")

func clearFileName(fileName string) string {
	index := strings.LastIndex(fileName, ".")
	if index == -1 {
		return fileName
	}
	return fileName[0:index]
}

func CutString(name string) string {
	prettyNameRegexp := regexp.MustCompile("[\\/\\\\\\*\\?\\:\"\\<\\>\\|]")
	name = prettyNameRegexp.ReplaceAllString(name, "")

	const (
		maxRunes         = 70
		maxBaseNameBytes = 251
	)
	runeCount := 0
	byteCount := 0
	for index, current := range name {
		runeBytes := utf8.RuneLen(current)
		if runeCount == maxRunes || byteCount+runeBytes > maxBaseNameBytes {
			return name[:index]
		}
		runeCount++
		byteCount += runeBytes
	}
	return name
}

func ClearFilmName(name string) string {
	if strings.HasSuffix(name, ".mp4") || strings.HasSuffix(name, ".jpg") {
		return name[:len(name)-4]
	}
	if strings.HasSuffix(name, ".") {
		return name[:len(name)-1]
	}
	return name
}

func AppendFilmName(name string) string {
	return ClearFilmName(name) + ".mp4"
}

func AppendImageName(name string) string {
	return ClearFilmName(name) + ".jpg"
}

func GetRealName(name string) string {
	return realNameRegexp.ReplaceAllString(clearFileName(name), "$1")
}

// SplitFilmPath splits the remainder of a path after the actor/collection level
// into a film group name and optional file name.
func SplitFilmPath(rest string) (groupName, fileName string) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", ""
	}
	parts := strings.SplitN(rest, "/", 2)
	groupName = parts[0]
	if len(parts) == 2 {
		fileName = parts[1]
	}
	return groupName, fileName
}
