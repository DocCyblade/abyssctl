package gendocs

import (
	"fmt"
	"os"
	"time"
)

func Banned() {
	_ = time.Now() // want "rule: clock"
	_ = os.WriteFile("f", nil, 0o600)
	f, _ := os.Create("f")
	_ = f.Close()
	fmt.Println("hi")
	_, _ = os.Stdout.WriteString("hi")
}
