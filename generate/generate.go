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
	f, err := os.Create(cachePath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(cachePath)
		return "", fmt.Errorf("write %q: %s", cachePath, err)
	}
	return cachePath, f.Close()
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
	codePointsStr := strings.TrimSpace(parts[0])
	tag = strings.TrimSpace(parts[1])
	if startStr, endStr, ok := strings.Cut(codePointsStr, ".."); ok {
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
	codePoints := strings.Split(codePointsStr, " ")
	emoji := make([]rune, len(codePoints))
	for i, codePointStr := range codePoints {
		codePoint, err := strconv.ParseInt(codePointStr, 16, 32)
		if err != nil {
			return nil, "", fmt.Errorf("failed to parse line %q: %s", line, err)
		}
		emoji[i] = rune(codePoint)
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

func collationData(cldrData *zip.Reader) (map[string]int, error) {
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
	collation := make(map[string]int)
	count := 1
	for _, line := range strings.Split(emojiCollation, "\n") {
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
				collation[removePresentationSelector(emoji)] = count
				count++
			}
		} else {
			return nil, fmt.Errorf("unexpected line format %q", line)
		}
	}
	return collation, nil
}

func collationOrder(e string, collationData map[string]int) int {
	for e != "" {
		if n, ok := collationData[removePresentationSelector(e)]; ok {
			return n
		}
		_, size := utf8.DecodeLastRuneInString(e)
		e = e[:len(e)-size]
	}
	return -1
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
	collation, err := collationData(&cldrData.Reader)
	if err != nil {
		return err
	}
	slices.SortFunc(emojis, func(e, f string) int {
		if n := collationOrder(e, collation) - collationOrder(f, collation); n != 0 {
			return n
		}
		return strings.Compare(e, f)
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
