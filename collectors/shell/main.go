// collectors/shell is an alias entry point for the shell collector.
// The shell hooks call "oc collector shell push ..." which routes through
// the main oc binary. This package exists for documentation clarity and
// can be used to build a standalone binary if needed.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "Use 'oc collector shell' instead of running this binary directly.")
	fmt.Fprintln(os.Stderr, "Example: oc collector shell install")
	os.Exit(1)
}
