package cmd

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestTransferNetworkErrorNeverFormatsUntrustedCause(t *testing.T) {
	target, err := url.Parse("https://storage.example/object?signature=target-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"upload", "download"} {
		for _, cause := range []error{
			errors.New(`failed to parse Location header "https://storage.example/%zz?signature=redirect-secret"`),
			&url.Error{Op: "Put", URL: target.String(), Err: errors.New("untrusted-secret")},
			context.Canceled,
			context.DeadlineExceeded,
		} {
			got := transferNetworkError(op, target, cause)
			visible := got.Error() + got.Message + got.Hint + string(got.Detail)
			for _, marker := range []string{"target-secret", "redirect-secret", "untrusted-secret", "%zz"} {
				if strings.Contains(visible, marker) {
					t.Fatalf("%s leaked cause: %s", op, visible)
				}
			}
			if !strings.Contains(got.Message, "storage.example") {
				t.Fatal("lost safe host diagnostic")
			}
		}
	}
	trusted := output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL", "unsafe redirect", "report the storage target")
	if got := transferNetworkError("upload", target, &url.Error{Err: trusted}); got != trusted {
		t.Fatal("trusted policy error was reclassified")
	}
}
