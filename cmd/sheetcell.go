package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// excelEpoch is the Univer / Excel serial-date origin (1899-12-30). A cell's
// numeric value counts days (with a fractional part for the time of day) from
// here; paired with a date/time number-format pattern it renders as a real date
// instead of the text that trips the "number stored as text" warning.
var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

// newSheetCellCmd builds `octo-cli sheet-cell`, an offline helper that turns a
// NATURAL value (an ISO date, a percentage, an amount…) into the Univer cell
// object `{v:<number>, s:{n:{pattern}}, t:2}` — the shape a real formatted cell
// needs. A bot writing to a sheet cannot be trusted to hand-assemble that object
// (it drops `s.n.pattern`/`t`, or writes a date as the text `"2025-01-10"`
// instead of serial 45667); this command does the number-format encoding and the
// date→serial arithmetic in code, deterministically. The bot supplies the value,
// takes `.data`, and drops it under a cell key in `docs sheet edit`.
func newSheetCellCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		date      string
		datetime  string
		percent   string
		currency  string
		thousands string
		number    string
		pattern   string
		symbol    string
	)

	cmd := &cobra.Command{
		Use:   "sheet-cell",
		Short: "Build a Univer sheet cell {v, s:{n:{pattern}}, t:2} from a natural value",
		Long: `Convert a natural value into the Univer spreadsheet cell object a bot must write
for a properly TYPED, FORMATTED cell — so dates/percent/currency land as real
numbers with a number format, not as text (which trips the "number stored as
text" warning). Pass exactly one value source:

  octo-cli sheet-cell --date 2025-01-10          # -> {"v":45667,"s":{"n":{"pattern":"yyyy-mm-dd"}},"t":2}
  octo-cli sheet-cell --datetime "2025-01-10 12:00"
  octo-cli sheet-cell --percent 25               # stores 0.25, pattern 0%
  octo-cli sheet-cell --currency 1200            # pattern ¥#,##0.00 (change with --symbol)
  octo-cli sheet-cell --currency 1200 --symbol '$'
  octo-cli sheet-cell --thousands 1234567        # pattern #,##0
  octo-cli sheet-cell --number 3.14 --pattern "0.00"   # any Excel format code (long tail)

--pattern overrides the default pattern of any source. Drop the result under a
cell key in a sheet edit:
  octo-cli docs sheet edit d_123 --base-version "$BV" \
    --data '{"cells":{"default!1:0": '"$(octo-cli sheet-cell --date 2025-01-10 --format json | jq -c .data)"'}}'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Exactly one value source must be given.
			srcFlags := []string{"date", "datetime", "percent", "currency", "thousands", "number"}
			var chosen []string
			for _, name := range srcFlags {
				if cmd.Flags().Changed(name) {
					chosen = append(chosen, "--"+name)
				}
			}
			if len(chosen) != 1 {
				ee := output.ErrValidation(
					fmt.Sprintf("need exactly one value source, got %d", len(chosen)),
					"pass one of --date --datetime --percent --currency --thousands --number",
				)
				_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
				return ee
			}

			var (
				v           float64
				defaultPatt string
				err         error
			)
			switch chosen[0] {
			case "--date":
				v, err = serialFromLayout("2006-01-02", date)
				defaultPatt = "yyyy-mm-dd"
			case "--datetime":
				v, err = serialFromDatetime(datetime)
				defaultPatt = "yyyy-mm-dd hh:mm"
			case "--percent":
				var p float64
				p, err = strconv.ParseFloat(percent, 64)
				v = p / 100 // 25 -> 0.25, the fraction Excel/Univer stores
				defaultPatt = "0%"
			case "--currency":
				v, err = strconv.ParseFloat(currency, 64)
				defaultPatt = symbol + "#,##0.00"
			case "--thousands":
				v, err = strconv.ParseFloat(thousands, 64)
				defaultPatt = "#,##0"
			case "--number":
				v, err = strconv.ParseFloat(number, 64)
				defaultPatt = "" // plain number unless --pattern is given
			}
			if err != nil {
				ee := output.ErrValidation(
					fmt.Sprintf("invalid value for %s: %v", chosen[0], err),
					"date/datetime want ISO (2025-01-10 / \"2025-01-10 12:00\"); numeric flags want a number",
				)
				_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
				return ee
			}

			// --pattern overrides the source's default.
			patt := defaultPatt
			if cmd.Flags().Changed("pattern") {
				patt = pattern
			}

			cell := map[string]any{
				"v": v,
				"t": 2, // CellValueType.NUMBER — the numfmt makes it display correctly
			}
			if patt != "" {
				cell["s"] = map[string]any{"n": map[string]any{"pattern": patt}}
			}
			buf, err := json.Marshal(cell)
			if err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}
			return f.EmitSuccess(buf)
		},
	}

	cmd.Flags().StringVar(&date, "date", "", "ISO date (YYYY-MM-DD) -> serial + yyyy-mm-dd")
	cmd.Flags().StringVar(&datetime, "datetime", "", "ISO date-time (\"YYYY-MM-DD HH:MM[:SS]\") -> serial + yyyy-mm-dd hh:mm")
	cmd.Flags().StringVar(&percent, "percent", "", "percentage (25 -> stores 0.25, pattern 0%)")
	cmd.Flags().StringVar(&currency, "currency", "", "amount -> pattern <symbol>#,##0.00")
	cmd.Flags().StringVar(&thousands, "thousands", "", "amount -> pattern #,##0 (thousands separator)")
	cmd.Flags().StringVar(&number, "number", "", "plain number; combine with --pattern for any Excel format code")
	cmd.Flags().StringVar(&pattern, "pattern", "", "override the number-format pattern (any Excel format code)")
	cmd.Flags().StringVar(&symbol, "symbol", "¥", "currency symbol for --currency")
	return cmd
}

// serialFromLayout parses value with the given layout and returns its Excel/Univer
// serial number (whole days from the 1899-12-30 epoch). Exact for any date from
// 1900-03-01 on, which covers every real-world date.
func serialFromLayout(layout, value string) (float64, error) {
	d, err := time.Parse(layout, value)
	if err != nil {
		return 0, err
	}
	return d.Sub(excelEpoch).Hours() / 24, nil
}

// serialFromDatetime parses an ISO date-time (with or without seconds) and
// returns its serial number — the day count plus the time-of-day as a fraction
// (12:00 -> +0.5), since Univer encodes time in the fractional part of the serial.
func serialFromDatetime(value string) (float64, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if d, err := time.Parse(layout, value); err == nil {
			return d.Sub(excelEpoch).Hours() / 24, nil
		}
	}
	return 0, fmt.Errorf("expected \"YYYY-MM-DD HH:MM\" or \"YYYY-MM-DD HH:MM:SS\", got %q", strings.TrimSpace(value))
}
