package memory

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"everything-you-need/m/services/note"
)

// MaxTextRunes is the longest memory text accepted.
const MaxTextRunes = 200

// minSecretDigits is the shortest digit run (separators ignored) treated as a
// secret: phone numbers, account numbers, OTPs, card and ID numbers.
const minSecretDigits = 6

// paddedWords returns the folded alphanumeric words of s joined by single
// spaces and padded with a space on both sides, so " phrase " can be searched
// as whole words.
func paddedWords(s string) string {
	w := strings.FieldsFunc(note.Fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return " " + strings.Join(w, " ") + " "
}

// secretWords are folded (no diacritics) whole-word phrases that mark credentials
// or identity/financial data. "pass" and "pin" are deliberately blunt: a false
// rejection only costs one fact, a false accept costs a leaked secret.
var secretWords = []string{
	"mat khau", "matkhau", "mk", "password", "passwd", "passcode", "pass", "otp", "ma pin", "pin", "stk",
	"so tai khoan", "tai khoan ngan hang", "cvv", "cvc", "cccd", "cmnd", "can cuoc", "the tin dung",
	"api key", "apikey", "secret key", "private key", "token", "seed phrase",
}

var ibanRe = regexp.MustCompile(`(?i)\b[a-z]{2}\d{2}(?:[ -]?[a-z0-9]{3,4}){3,8}\b`)

// SecretLike reports whether text looks like a secret or sensitive identifier
// and must never be stored: any run of 6+ digits (spaces, dashes, dots, commas
// and underscores between digits are ignored, so "12 34-56" counts as 6),
// Luhn-valid card numbers, IBAN-like strings, CCCD/CMND numbers (12 and 9
// digits, covered by the digit rule) and credential keywords in Vietnamese
// (with or without accents) and English.
func SecretLike(text string) bool {
	if maxDigitRun(text) >= minSecretDigits || cardLike(text) || ibanRe.MatchString(text) {
		return true
	}
	pw := paddedWords(text)
	for _, k := range secretWords {
		if strings.Contains(pw, " "+k+" ") {
			return true
		}
	}
	return false
}

func isDigitSep(r rune) bool {
	return r == ' ' || r == '-' || r == '.' || r == ',' || r == '_' || r == ' '
}

// digitRuns returns the digit strings of text; separators between digits do not
// break a run, any other rune does.
func digitRuns(text string) []string {
	var runs []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			runs = append(runs, string(cur))
		}
		cur = nil
	}
	for _, r := range text {
		switch {
		case r >= '0' && r <= '9':
			cur = append(cur, r)
		case isDigitSep(r) && len(cur) > 0:
		default:
			flush()
		}
	}
	flush()
	return runs
}

func maxDigitRun(text string) int {
	m := 0
	for _, r := range digitRuns(text) {
		if len(r) > m {
			m = len(r)
		}
	}
	return m
}

func cardLike(text string) bool {
	for _, r := range digitRuns(text) {
		if len(r) >= 13 && len(r) <= 19 && luhnValid(r) {
			return true
		}
	}
	return false
}

// luhnValid applies the Luhn checksum to a string of ASCII digits.
func luhnValid(digits string) bool {
	sum, alt := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i]) - '0'
		if d < 0 || d > 9 {
			return false
		}
		if alt {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return len(digits) > 0 && sum%10 == 0
}

// instructionPhrases are folded whole-word phrases that address the bot or try
// to set rules: a memory is a fact about the user, never an order.
//
// Folding maps different Vietnamese words onto one string ("hay" = hay/hãy,
// "ban la" = bạn là/bàn là, "chi dan" = chỉ dẫn/chị Dần), so a bare folded "hay"
// or a short folded pair that is also an ordinary phrase must NOT be listed here.
// Those imperatives are matched on the accented form by accentedInstructionRe;
// the list below keeps only multi-word phrases that are unambiguous when
// unaccented ("hay tra loi", "hay bo qua").
var instructionPhrases = []string{
	"bo qua", "quen het", "luon luon", "quy tac", "tu bay gio",
	"hay tra loi", "hay bo qua", "hay quen", "tra loi bang", "ban phai", "bot phai", "dong vai",
	"ignore", "disregard", "forget", "always", "never", "rule", "rules", "system prompt", "system message",
	"instruction", "instructions", "you must", "you should", "you are", "you will", "from now on",
	"do not", "dont", "must", "pretend", "act as", "roleplay", "role play", "jailbreak", "developer mode",
	"override", "respond", "reply with", "answer with",
}

// accentedInstructionRe matches imperative markers that are only unambiguous
// with their diacritics (checked on the lower-cased, unfolded text): "hãy ...",
// "bạn là/hãy/phải ...", "chỉ dẫn". "hay" (often / or) never matches.
var accentedInstructionRe = regexp.MustCompile(`(^|[^\p{L}])(hãy|bạn là|bạn hãy|bạn phải|chỉ dẫn|chỉ thị)([^\p{L}]|$)`)

var urlRe = regexp.MustCompile(`(?i)(://|\bwww\.|\b[a-z0-9-]+\.(?:com|net|org|vn|io|app|dev|me|ly|co|info|xyz|gov|edu|ai|tk|ru|cn)\b)`)

// InstructionLike reports whether text must not be stored: empty text, text over
// MaxTextRunes runes, control or invisible format characters, markup (code
// fences, backticks, brackets, headings, bold), URLs, or an imperative aimed at
// the bot or about rules (Vietnamese and English, with or without accents).
func InstructionLike(text string) bool {
	if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > MaxTextRunes {
		return true
	}
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' {
			return true
		}
		switch r {
		case '`', '<', '>', '{', '}', '[', ']', '\\', '|':
			return true
		}
	}
	t := strings.TrimSpace(text)
	if strings.Contains(t, "**") || strings.Contains(t, "__") || strings.Contains(t, "~~") ||
		strings.HasPrefix(t, "#") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
		return true
	}
	if urlRe.MatchString(text) {
		return true
	}
	if accentedInstructionRe.MatchString(strings.ToLower(text)) {
		return true
	}
	pw := paddedWords(text)
	for _, p := range instructionPhrases {
		if strings.Contains(pw, " "+p+" ") {
			return true
		}
	}
	return false
}

// Sanitize prepares extracted text for storage: it strips the characters the
// render block uses as delimiters (backticks, angle brackets), collapses all
// whitespace including newlines to single spaces, trims, then applies SecretLike
// and InstructionLike. ok is false when the text must be rejected; the returned
// string is only meaningful when ok is true.
func Sanitize(text string) (string, bool) {
	text = strings.NewReplacer("`", "", "<", "", ">", "").Replace(text)
	text = strings.Join(strings.Fields(text), " ")
	if text == "" || SecretLike(text) || InstructionLike(text) {
		return "", false
	}
	return text, true
}
