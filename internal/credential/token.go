package credential

// Token prefixes route a bot token to its kind. app_* is an App Bot; bf_* is a
// User Bot. The prefix is the only thing the CLI inspects — routing is otherwise
// server-side.
const (
	prefixApp  = "app_"
	prefixUser = "bf_"
)

// TokenKind classifies a token by its prefix: "app_bot", "user_bot", or
// "unknown". An empty token returns "".
func TokenKind(tok string) string {
	switch {
	case tok == "":
		return ""
	case hasPrefix(tok, prefixApp):
		return "app_bot"
	case hasPrefix(tok, prefixUser):
		return "user_bot"
	}
	return "unknown"
}

// Masking parameters: how much of the token body to reveal around the masked
// middle, and the minimum number of body chars that must stay masked for any
// reveal to happen. Tuned so two tokens are distinguishable (kind prefix + a
// little head + last 4) without exposing the secret; the middle is always a
// fixed "***" so the token's length is not revealed.
const (
	maskHead      = 2
	maskTail      = 4
	maskMinMiddle = 3
)

// MaskToken renders a token for display: the kind prefix, a couple of leading
// body chars, a fixed "***", and the last few chars — e.g. "app_1a***7g8h".
// Tokens too short to reveal head and tail while keeping the middle masked fall
// back to just the kind prefix + "***". An empty token returns "".
func MaskToken(tok string) string {
	if tok == "" {
		return ""
	}
	prefix := ""
	switch {
	case hasPrefix(tok, prefixApp):
		prefix = prefixApp
	case hasPrefix(tok, prefixUser):
		prefix = prefixUser
	}
	body := tok[len(prefix):]
	if len(body) < maskHead+maskTail+maskMinMiddle {
		return prefix + "***"
	}
	return prefix + body[:maskHead] + "***" + body[len(body)-maskTail:]
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
