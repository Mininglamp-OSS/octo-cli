package credential

// Token prefixes describe the credential format. They never determine a Loop
// principal class; Fleet derives human/device/execution identity only after
// server-side verification.
const (
	prefixApp     = "app_"
	prefixUser    = "bf_"
	prefixUserKey = "uk_"
	prefixLoop    = "octo_loop_"
)

// TokenKind classifies only the token format. An empty token returns "".
func TokenKind(tok string) string {
	switch {
	case tok == "":
		return ""
	case hasPrefix(tok, prefixApp):
		return "app_bot"
	case hasPrefix(tok, prefixUser):
		return "user_bot"
	case hasPrefix(tok, prefixUserKey):
		return "user_key"
	case hasPrefix(tok, prefixLoop):
		return "loop_credential"
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
	var prefix string
	switch {
	case hasPrefix(tok, prefixApp):
		prefix = prefixApp
	case hasPrefix(tok, prefixUser):
		prefix = prefixUser
	case hasPrefix(tok, prefixUserKey):
		prefix = prefixUserKey
	case hasPrefix(tok, prefixLoop):
		prefix = prefixLoop
	default:
		// Unknown kind: reveal nothing. Without a recognized prefix there is no
		// way to say what the revealed head/tail belong to, so don't leak it.
		return "***"
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
