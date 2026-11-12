package swurepackage

import (
	"fmt"
	"strconv"
	"strings"
	"text/scanner"
	"unicode"
)

func reservedFIP(name string) bool {
	return !strings.Contains(name, "/") && strings.HasPrefix(name, "fip-") && strings.HasSuffix(name, ".bin")
}

type configToken struct {
	text             string
	start, end, mate int
}

type configField struct{ start, end, value, valueEnd int }
type imageRecord struct {
	start, end int
	fields     map[string]configField
}
type spanEdit struct {
	start, end int
	text       string
}

func configTokens(source string) ([]configToken, error) {
	var s scanner.Scanner
	s.Init(strings.NewReader(source))
	s.Mode = scanner.ScanIdents | scanner.ScanStrings | scanner.ScanInts | scanner.ScanComments | scanner.SkipComments
	s.IsIdentRune = func(r rune, i int) bool {
		return unicode.IsLetter(r) || r == '_' || i > 0 && (unicode.IsDigit(r) || r == '-')
	}
	var scanErr error
	s.Error = func(s *scanner.Scanner, msg string) {
		scanErr = fmt.Errorf("sw-description at %s: %s", s.Position, msg)
	}
	var tokens []configToken
	var stack []int
	for tok := s.Scan(); tok != scanner.EOF; tok = s.Scan() {
		if tok == '#' {
			for s.Peek() != '\n' && s.Peek() != scanner.EOF {
				s.Next()
			}
			continue
		}
		text := s.TokenText()
		i := len(tokens)
		tokens = append(tokens, configToken{text: text, start: s.Position.Offset, end: s.Position.Offset + len(text), mate: -1})
		if text == "(" || text == "[" || text == "{" {
			stack = append(stack, i)
		}
		if text == ")" || text == "]" || text == "}" {
			if len(stack) == 0 {
				return nil, fmt.Errorf("unbalanced sw-description")
			}
			j := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if tokens[j].text+text != "()" && tokens[j].text+text != "[]" && tokens[j].text+text != "{}" {
				return nil, fmt.Errorf("unbalanced sw-description")
			}
			tokens[j].mate = i
			tokens[i].mate = j
		}
	}
	if scanErr != nil {
		return nil, scanErr
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("unbalanced sw-description")
	}
	return tokens, nil
}

func recordFields(tokens []configToken, start int) (imageRecord, error) {
	r := imageRecord{start: start, end: tokens[start].mate, fields: make(map[string]configField)}
	for i := start + 1; i < r.end; {
		key := tokens[i].text
		if i+2 >= r.end || (tokens[i+1].text != "=" && tokens[i+1].text != ":") {
			return r, fmt.Errorf("invalid image field %q", key)
		}
		if _, ok := r.fields[key]; ok {
			return r, fmt.Errorf("duplicate image field %q", key)
		}
		value := i + 2
		end := value
		for end < r.end && tokens[end].text != ";" && tokens[end].text != "," {
			if end > value && end+1 < r.end && (tokens[end+1].text == "=" || tokens[end+1].text == ":") {
				break
			}
			if tokens[end].mate > end {
				end = tokens[end].mate + 1
			} else {
				end++
			}
		}
		valueEnd := end
		if end < r.end && (tokens[end].text == ";" || tokens[end].text == ",") {
			end++
		}
		r.fields[key] = configField{start: i, end: end, value: value, valueEnd: valueEnd}
		i = end
	}
	return r, nil
}

func fieldString(tokens []configToken, r imageRecord, key string) (string, error) {
	f, ok := r.fields[key]
	if !ok || f.valueEnd != f.value+1 {
		return "", fmt.Errorf("image requires one direct %s string", key)
	}
	value, err := strconv.Unquote(tokens[f.value].text)
	if err != nil || value == "" {
		return "", fmt.Errorf("image requires nonempty %s string", key)
	}
	return value, nil
}

func applySpans(source string, edits []spanEdit) string {
	// Callers supply non-overlapping edits in source order.
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		source = source[:e.start] + e.text + source[e.end:]
	}
	return source
}

func rewriteRoot(source string, tokens []configToken, r imageRecord, p imagePayload, split bool) (string, string, error) {
	device, err := fieldString(tokens, r, "device")
	if err != nil {
		return "", "", err
	}
	if _, err := fieldString(tokens, r, "sha256"); err != nil {
		return "", "", err
	}
	if split {
		typ, err := fieldString(tokens, r, "type")
		if err != nil || typ != "raw" {
			return "", "", fmt.Errorf("split rootfs requires explicit raw type")
		}
		for key := range r.fields {
			if key == "hook" || key == "ref" || key == "encrypted" || key == "ivt" || key == "encryption" || key == "encrypt" || strings.HasPrefix(key, "install-if-") {
				return "", "", fmt.Errorf("split rootfs cannot use %s", key)
			}
		}
	}
	owned := map[string]string{"filename": strconv.Quote(p.name), "sha256": strconv.Quote(p.hash), "offset": strconv.Quote(strconv.FormatInt(p.offset, 10)), "type": `"raw"`, "installed-directly": "true"}
	if p.compressed {
		owned["compressed"] = `"zlib"`
	} else {
		owned["compressed"] = ""
	}
	start, end := tokens[r.start].start, tokens[r.end].end
	var edits []spanEdit
	// Iterate tokens, not maps: edits remain ordered and deterministic.
	for i := r.start + 1; i < r.end; {
		key := tokens[i].text
		f := r.fields[key]
		if value, ok := owned[key]; ok {
			if value == "" {
				edits = append(edits, spanEdit{tokens[f.start].start - start, tokens[f.end-1].end - start, ""})
			} else {
				edits = append(edits, spanEdit{tokens[f.value].start - start, tokens[f.valueEnd-1].end - start, value})
			}
			delete(owned, key)
		}
		i = f.end
	}
	var added strings.Builder
	for _, key := range []string{"filename", "sha256", "offset", "type", "installed-directly", "compressed"} {
		if value := owned[key]; value != "" {
			fmt.Fprintf(&added, "\n                        %s = %s;", key, value)
		}
	}
	if added.Len() != 0 {
		edits = append(edits, spanEdit{tokens[r.end].start - start, tokens[r.end].start - start, added.String() + "\n                    "})
	}
	return applySpans(source[start:end], edits), device, nil
}

func metadataRecord(p imagePayload, device string) string {
	return fmt.Sprintf("{ filename = %s; sha256 = %s; device = %s; type = \"raw\"; installed-directly = true; offset = %s; }", strconv.Quote(p.name), strconv.Quote(p.hash), strconv.Quote(device), strconv.Quote(strconv.FormatInt(p.offset, 10)))
}

// rewriteDescription edits only direct images records. Token spans preserve
// unrelated attributes, nested properties, scripts, and bootloader selections.
func rewriteDescription(source, oldRootfsName string, payloads []imagePayload) (string, bool, error) {
	tokens, err := configTokens(source)
	if err != nil {
		return "", false, err
	}
	oldRoot := strings.TrimSuffix(oldRootfsName, ".gz")
	bodyName, tailName := oldRoot+".avb.body", oldRoot+".avb.tail"
	var edits []spanEdit
	matches, pairs := 0, 0
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].text != "images" || (tokens[i+1].text != ":" && tokens[i+1].text != "=") {
			continue
		}
		open := i + 2
		if tokens[open].text != "(" {
			return "", false, fmt.Errorf("images must be a list")
		}
		close := tokens[open].mate
		var records []imageRecord
		for j := open + 1; j < close; {
			if tokens[j].text == "," {
				j++
				continue
			}
			if tokens[j].text != "{" {
				return "", false, fmt.Errorf("images requires direct records")
			}
			r, err := recordFields(tokens, j)
			if err != nil {
				return "", false, err
			}
			records = append(records, r)
			j = r.end + 1
		}
		rootIndex, bodyIndex, tailIndex := -1, -1, -1
		for j, r := range records {
			name, _ := fieldString(tokens, r, "filename")
			switch name {
			case oldRootfsName:
				if rootIndex >= 0 {
					return "", false, fmt.Errorf("duplicate rootfs in images list")
				}
				rootIndex = j
			case bodyName:
				if bodyIndex >= 0 {
					return "", false, fmt.Errorf("duplicate owned AVB body")
				}
				bodyIndex = j
			case tailName:
				if tailIndex >= 0 {
					return "", false, fmt.Errorf("duplicate owned AVB tail")
				}
				tailIndex = j
			}
		}
		if rootIndex < 0 {
			if bodyIndex >= 0 || tailIndex >= 0 {
				return "", false, fmt.Errorf("misplaced owned AVB pair")
			}
			i = close
			continue
		}
		matches++
		root, device, err := rewriteRoot(source, tokens, records[rootIndex], payloads[0], len(payloads) > 1)
		if err != nil {
			return "", false, err
		}
		if bodyIndex >= 0 || tailIndex >= 0 {
			if bodyIndex != rootIndex+1 || tailIndex != rootIndex+2 {
				return "", false, fmt.Errorf("partial or misplaced owned AVB pair")
			}
			for _, index := range []int{bodyIndex, tailIndex} {
				r := records[index]
				d, err := fieldString(tokens, r, "device")
				if err != nil || d != device {
					return "", false, fmt.Errorf("owned AVB device disagreement")
				}
				if _, err := fieldString(tokens, r, "sha256"); err != nil {
					return "", false, err
				}
			}
			pairs++
		}
		if bodyIndex < 0 {
			for _, p := range payloads[1:] {
				root += ",\n                    " + metadataRecord(p, device)
			}
		}
		edits = append(edits, spanEdit{tokens[records[rootIndex].start].start, tokens[records[rootIndex].end].end, root})
		if bodyIndex >= 0 {
			if len(payloads) == 1 {
				edits = append(edits, spanEdit{tokens[records[rootIndex].end].end, tokens[records[tailIndex].end].end, ""})
			} else {
				for j, index := range []int{bodyIndex, tailIndex} {
					r := records[index]
					edits = append(edits, spanEdit{tokens[r.start].start, tokens[r.end].end, metadataRecord(payloads[j+1], device)})
				}
			}
		}
		i = close
	}
	if matches == 0 {
		return "", false, fmt.Errorf("rootfs %q not found in images", oldRootfsName)
	}
	if pairs != 0 && pairs != matches {
		return "", false, fmt.Errorf("partial owned AVB pairs across rootfs selections")
	}
	return applySpans(source, edits), pairs > 0, nil
}

// rewriteSoftwareVersion changes only software.version, retaining the seed variant.
func rewriteSoftwareVersion(source, version string) (string, error) {
	tokens, err := configTokens(source)
	if err != nil {
		return "", err
	}
	if len(tokens) < 3 || tokens[0].text != "software" || tokens[2].text != "{" {
		return "", fmt.Errorf("SWU manifest requires a software group")
	}
	record, err := recordFields(tokens, 2)
	if err != nil {
		return "", err
	}
	old, err := fieldString(tokens, record, "version")
	if err != nil {
		return "", err
	}
	for _, suffix := range []string{"-release", "-mfg", "-dev"} {
		if strings.HasSuffix(old, suffix) {
			version = strings.TrimSuffix(version, suffix) + suffix
			break
		}
	}
	field := record.fields["version"]
	return applySpans(source, []spanEdit{{tokens[field.value].start, tokens[field.valueEnd-1].end, strconv.Quote(version)}}), nil
}
