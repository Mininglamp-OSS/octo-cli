package mcp

import "fmt"

func xin2549Params(tool string, dry bool) string {
	field := ""
	if tool == "execute" && dry {
		field = `,"dry_run":true`
	}
	return fmt.Sprintf(`{"name":%q,"arguments":{"operation_id":"message.send","arguments":{"channel_id":"fixture","channel_type":1,"payload":{"text":"inert"}}%s}}`, tool, field)
}
