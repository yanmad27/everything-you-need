// Command adminhash prints a bcrypt hash for ADMIN_PASSWORD_HASH and, on a second line, its
// base64 form as ADMIN_PASSWORD_HASH_B64 (no `$`; use this on compose/Dokploy). The password
// is read from stdin (never from argv) and is never logged or echoed.
//
//	printf '%s' 'my password' | go run ./cmd/adminhash
//	go run ./cmd/adminhash        # prompts; input is visible unless piped
package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprintln(os.Stderr, "Password (input is visible; prefer piping from a password manager):")
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "no password on stdin")
		os.Exit(1)
	}
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < 12 || len(pw) > 72 {
		fmt.Fprintln(os.Stderr, "password must be 12-72 bytes")
		os.Exit(1)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hashing failed")
		os.Exit(1)
	}
	fmt.Println(string(h))
	fmt.Println("ADMIN_PASSWORD_HASH_B64=" + base64.RawURLEncoding.EncodeToString(h))
}
