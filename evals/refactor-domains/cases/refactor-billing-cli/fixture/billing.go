package billing

import (
	"fmt"
	"io"
	"strconv"

	"billing/internal/store"
	"billing/internal/util"
)

// Run is the whole CLI: issue, pay and statement.
func Run(args []string, out, errOut io.Writer, dir string) int {
	if len(args) < 2 {
		fmt.Fprintln(errOut, "usage: billing issue|pay|statement <customer> [cents]")
		return 2
	}
	mgr := store.NewManager(dir)
	switch args[0] {
	case "issue", "pay":
		if len(args) < 3 {
			fmt.Fprintln(errOut, "usage: billing "+args[0]+" <customer> <cents>")
			return 2
		}
		cents, err := strconv.Atoi(args[2])
		if err != nil || cents <= 0 {
			fmt.Fprintln(errOut, "cents must be a positive number")
			return 2
		}
		if args[0] == "issue" {
			num, err := mgr.IssueInvoice(args[1], cents)
			if err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			fmt.Fprintf(out, "issued invoice %d for %s\n", num, util.FormatMoney(cents))
			return 0
		}
		if err := mgr.RecordPayment(args[1], cents); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		fmt.Fprintf(out, "recorded payment of %s\n", util.FormatMoney(cents))
		return 0
	case "statement":
		text, err := mgr.RenderStatement(args[1])
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		fmt.Fprint(out, text)
		return 0
	}
	fmt.Fprintf(errOut, "unknown command %q\n", args[0])
	return 2
}
