package cmdutil

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// ParseInput reads an input source specifier and returns the raw bytes. The
// spec mirrors curl/gh behaviour:
//   - "@-"          read from f.IOStreams.In (stdin)
//   - "@filepath"   read from the named file
//   - anything else return the string's bytes as-is
//
// An empty spec returns nil (no input).
func ParseInput(f *Factory, spec string) ([]byte, error) {
	if spec == "" {
		return nil, nil
	}
	if spec == "@-" {
		if f == nil || f.IOStreams == nil || f.IOStreams.In == nil {
			return nil, fmt.Errorf("no stdin available for @-")
		}
		return readAll(f.IOStreams.In)
	}
	if strings.HasPrefix(spec, "@") {
		path := spec[1:]
		if path == "" {
			return nil, fmt.Errorf("empty file path after @")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return data, nil
	}
	return []byte(spec), nil
}

func readAll(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return data, nil
}
