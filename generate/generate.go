package main

import (
	"archive/zip"
	"bufio"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	emojiData         = flag.String("emoji-data", "https://www.unicode.org/Public/16.0.0/ucd/emoji/emoji-data.txt", "URL for emoji data file")
	emojiSequences    = flag.String("emoji-sequences", "https://www.unicode.org/Public/emoji/16.0/emoji-sequences.txt", "URL for emoji sequences file")
	emojiZWJSequences = flag.String("emoji-zwj-sequences", "https://www.unicode.org/Public/emoji/16.0/emoji-zwj-sequences.txt", "URL for emoji ZWJ sequences file")
	cldr              = flag.String("cldr", "https://unicode.org/Public/cldr/46/cldr-common-46.0.zip", "URL for CLDR data")
	c                 = flag.String("c", "", "Cache dir")
)

func getCached(url, cacheDir string) (string, error) {
	cachePath := cacheDir + "/" + path.Base(url)
	if _, err := os.Stat(cachePath); !errors.Is(err, fs.ErrNotExist) {
		return cachePath, nil
	}
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get %q: %s", url, resp.Status)
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}
	f, err := os.Create(cachePath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(cachePath)
		return "", fmt.Errorf("write %q: %s", cachePath, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(cachePath)
		return "", err
	}
	return cachePath, nil
}

func parseEmojiDataLine(line string) (emojis []string, tag string, err error) {
	line, _, _ = strings.Cut(line, "#")
	if line == "" {
		return nil, "", nil
	}
	parts := strings.SplitN(line, ";", 3)
	if len(parts) < 2 {
		return nil, "", fmt.Errorf("parse line %q: %s", line, err)
	}
	codepointsStr := strings.TrimSpace(parts[0])
	tag = strings.TrimSpace(parts[1])
	if startStr, endStr, ok := strings.Cut(codepointsStr, ".."); ok {
		start, err := strconv.ParseInt(startStr, 16, 32)
		if err != nil {
			return nil, "", fmt.Errorf("failed to parse line %q: %s", line, err)
		}
		end, err := strconv.ParseInt(endStr, 16, 32)
		if err != nil {
			return nil, "", fmt.Errorf("failed to parse line %q: %s", line, err)
		}
		emojis := make([]string, 0, end-start+1)
		for i := start; i <= end; i++ {
			emojis = append(emojis, string([]rune{rune(i)}))
		}
		return emojis, tag, nil
	}
	codepoints := strings.Split(codepointsStr, " ")
	emoji := make([]rune, len(codepoints))
	for i, codepointStr := range codepoints {
		codepoint, err := strconv.ParseInt(codepointStr, 16, 32)
		if err != nil {
			return nil, "", fmt.Errorf("failed to parse line %q: %s", line, err)
		}
		emoji[i] = rune(codepoint)
	}
	return []string{string(emoji)}, tag, nil
}

func emojiModifiers(cacheDir string) ([]string, error) {
	var modifiers []string
	filePath, err := getCached(*emojiData, cacheDir)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		emojis, tag, err := parseEmojiDataLine(line)
		if err != nil {
			return nil, err
		}
		if tag == "Emoji_Modifier" {
			modifiers = append(modifiers, emojis...)
		}
	}
	return modifiers, scanner.Err()
}

func emojisInFile(url string, modifiers map[string]bool, cacheDir string) ([]string, error) {
	file, err := getCached(url, cacheDir)
	if err != nil {
		return nil, err
	}
	var emojis []string
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		lineEmojis, _, err := parseEmojiDataLine(line)
		if err != nil {
			return nil, err
		}
		for _, e := range lineEmojis {
			if !modifiers[e] {
				emojis = append(emojis, e)
			}
		}
	}
	return emojis, scanner.Err()
}

func emojis(cacheDir string) ([]string, error) {
	modifiersSlice, err := emojiModifiers(cacheDir)
	if err != nil {
		return nil, err
	}
	modifiers := make(map[string]bool, len(modifiersSlice))
	for _, m := range modifiersSlice {
		modifiers[m] = true
	}
	sequences, err := emojisInFile(*emojiSequences, modifiers, cacheDir)
	if err != nil {
		return nil, err
	}
	zwjSequences, err := emojisInFile(*emojiZWJSequences, modifiers, cacheDir)
	if err != nil {
		return nil, err
	}
	return append(sequences, zwjSequences...), nil
}

func annotationsInFile(f io.Reader) (map[string]string, error) {
	contents, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read CLDR data: %s", err)
	}
	var annotations struct {
		Annotations []struct {
			CP         string `xml:"cp,attr"`
			Type       string `xml:"type,attr"`
			Annotation string `xml:",chardata"`
		} `xml:"annotations>annotation"`
	}
	if err := xml.Unmarshal(contents, &annotations); err != nil {
		return nil, fmt.Errorf("parse CLDR data: %s", err)
	}

	type annotationData struct {
		name        string
		annotations []string
	}
	emojiAnnotations := make(map[string]annotationData)
	for _, annotation := range annotations.Annotations {
		annotationData := emojiAnnotations[annotation.CP]
		if annotation.Type == "tts" {
			annotationData.name = annotation.Annotation
		} else {
			annotations := strings.Split(annotation.Annotation, "|")
			for i, a := range annotations {
				annotations[i] = strings.TrimSpace(a)
			}
			annotationData.annotations = annotations
		}
		emojiAnnotations[annotation.CP] = annotationData
	}
	annotationsCombined := make(map[string]string, len(emojiAnnotations))
	for emoji, annotation := range emojiAnnotations {
		annotation.annotations = slices.DeleteFunc(annotation.annotations, func(s string) bool { return strings.Contains(annotation.name, s) })
		annotationsStr := strings.ReplaceAll(annotation.name, ": ", " ")
		annotationsStr = strings.ReplaceAll(annotationsStr, ", ", " ")
		if len(annotation.annotations) > 0 {
			annotationsStr += " " + strings.Join(annotation.annotations, " ")
		}
		annotationsCombined[emoji] = annotationsStr
	}
	return annotationsCombined, nil
}

func annotations(cldrData *zip.Reader) (map[string]string, error) {
	annotationsFile, err := cldrData.Open("common/annotations/en.xml")
	if err != nil {
		return nil, fmt.Errorf("read CLDR data: %s", err)
	}
	defer annotationsFile.Close()
	annotations, err := annotationsInFile(annotationsFile)
	if err != nil {
		return nil, err
	}
	annotationsDerivedFile, err := cldrData.Open("common/annotationsDerived/en.xml")
	if err != nil {
		return nil, fmt.Errorf("read CLDR data: %s", err)
	}
	defer annotationsDerivedFile.Close()
	annotationsDerived, err := annotationsInFile(annotationsDerivedFile)
	if err != nil {
		return nil, err
	}
	maps.Copy(annotations, annotationsDerived)
	return annotations, err
}

func removePresentationSelector(emoji string) string {
	return strings.ReplaceAll(emoji, "\ufe0f", "")
}

func collationData(cldrData *zip.Reader) (func(string) int, error) {
	file, err := cldrData.Open("common/collation/root.xml")
	if err != nil {
		return nil, fmt.Errorf("read CLDR data: %s", err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read CLDR data: %s", err)
	}
	var collations struct {
		Collation []struct {
			Type string `xml:"type,attr"`
			CR   string `xml:"cr"`
		} `xml:"collations>collation"`
	}
	if err := xml.Unmarshal(content, &collations); err != nil {
		return nil, fmt.Errorf("parse CLDR data %q: %s", file, err)
	}
	var emojiCollation string
	for _, collation := range collations.Collation {
		if collation.Type == "emoji" {
			emojiCollation = collation.CR
			break
		}
	}
	if emojiCollation == "" {
		return nil, fmt.Errorf("no emoji collation found in %q", file)
	}
	// 🫤
	ignorable := make(map[rune]bool)
	collation := make(map[string]int)
	count := 1
	for _, line := range strings.Split(emojiCollation, "\n") {
		if line, ok := strings.CutPrefix(line, "& [last primary ignorable]<<*"); ok {
			for _, rune := range line {
				ignorable[rune] = true
			}
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "&") {
			continue
		}
		if strings.HasPrefix(line, "<*") {
			for _, emoji := range line[2:] {
				collation[string([]rune{emoji})] = count
				count++
			}
		} else if strings.HasPrefix(line, "<") {
			for _, emoji := range strings.FieldsFunc(line[1:], func(r rune) bool { return r == ' ' || r == '<' || r == '=' || r == '\'' }) {
				collation[emoji] = count
				count++
			}
		} else {
			return nil, fmt.Errorf("unexpected line format %q", line)
		}
	}
	return func(emoji string) int {
		// First try the minimally qualified version, with one trailing
		// modifier removed.
		minimallyQualified := removePresentationSelector(emoji)
		lastRune, size := utf8.DecodeLastRuneInString(minimallyQualified)
		if size < len(minimallyQualified) && ignorable[lastRune] {
			minimallyQualified = minimallyQualified[:len(minimallyQualified)-size]
			minimallyQualified = strings.TrimSuffix(minimallyQualified, "\u200d") // ZWJ
		}
		if n, ok := collation[minimallyQualified]; ok {
			return n
		}
		// If that's not found, try the fully unqualified version,
		// where we remove all modifiers.
		unqualified := minimallyQualified
		var unqualifiedRunes []rune
		for _, r := range unqualified {
			if !ignorable[r] {
				unqualifiedRunes = append(unqualifiedRunes, r)
			}
		}
		unqualified = string(unqualifiedRunes)
		unqualified = strings.TrimRight(unqualified, "\u200d") // ZWJ
		if n, ok := collation[unqualified]; ok {
			return n
		}
		// Finally, fall back to checking the first codepoint.
		firstRune, _ := utf8.DecodeRuneInString(emoji)
		if n, ok := collation[string([]rune{firstRune})]; ok {
			return n
		}
		fmt.Fprintf(os.Stderr, "Unable to classify emoji %s", emoji)
		os.Exit(1)
		panic("unreachable")
	}, nil
}

func generate() error {
	cacheDir := *c
	if cacheDir == "" {
		var err error
		cacheDir, err = os.MkdirTemp("", "")
		if err != nil {
			return err
		}
		defer os.RemoveAll(cacheDir)
	}
	emojis, err := emojis(cacheDir)
	if err != nil {
		return err
	}

	cldrFile, err := getCached(*cldr, cacheDir)
	if err != nil {
		return err
	}
	cldrData, err := zip.OpenReader(cldrFile)
	if err != nil {
		return err
	}
	defer cldrData.Close()
	annotations, err := annotations(&cldrData.Reader)
	if err != nil {
		return err
	}
	collate, err := collationData(&cldrData.Reader)
	if err != nil {
		return err
	}
	slices.SortFunc(emojis, func(e, f string) int {
		if n := collate(e) - collate(f); n != 0 {
			return n
		}
		if n := strings.Count(e, "\u200d") - strings.Count(f, "\u200d"); n != 0 {
			return n
		}
		return slices.Compare([]rune(e), []rune(f))
	})
	for _, emoji := range emojis {
		// From CLDR: "Warnings: All cp values have U+FE0F characters removed."
		// So we have to remove all fe0f characters for some reason.
		annotation, ok := annotations[removePresentationSelector(emoji)]
		if !ok {
			return fmt.Errorf("emoji %q has no annotation", emoji)
		}
		fmt.Println(emoji, annotation)
	}
	return nil
}

func main() {
	flag.Parse()

	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
