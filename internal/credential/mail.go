package credential

// MailCredential is an account-scoped credential issued for one Agent Mail
// binding. It is not a Bot token and must never be used against Octo Bot APIs.
type MailCredential struct {
	Token      string
	BotID      string
	BotProfile string
	Source     string
}
