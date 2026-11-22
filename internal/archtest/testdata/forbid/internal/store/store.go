package store

import (
	"fmt"
	"os"
	"time"
)

func Banned() {
	_ = time.Now()                    // want "rule: clock"
	_ = os.WriteFile("f", nil, 0o600) // want "rule: write"
	f, _ := os.Create("f")            // want "rule: write"
	_ = f.Close()
	fmt.Println("hi")                  // want "rule: print"
	_, _ = os.Stdout.WriteString("hi") // want "rule: print"
}
