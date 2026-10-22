// Command botconfig-rotate re-encrypts stored bot secrets from one
// BOTCONFIG_SECRET to another. Secrets come from the environment (never argv):
//
//	BOTCONFIG_OLD_SECRET=... BOTCONFIG_SECRET=... go run ./cmd/botconfig-rotate -db data/convo.db
//
// Stop the bot first (it holds the database). Nothing is written if any stored
// secret fails to decrypt with the old secret.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"everything-you-need/m/services/botconfig"
	"everything-you-need/m/services/convo"
)

func main() {
	db := flag.String("db", "data/convo.db", "path to the convo SQLite database")
	flag.Parse()
	st, err := convo.Open(*db, convo.Options{NoBackground: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer st.Close()
	n, err := botconfig.Rotate(context.Background(), st, os.Getenv("BOTCONFIG_OLD_SECRET"), os.Getenv("BOTCONFIG_SECRET"), "rotate-cli")
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotate:", err)
		os.Exit(1)
	}
	fmt.Printf("re-encrypted %d secret(s)\n", n)
}
