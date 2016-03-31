package safe

import (
	"log"
	"runtime/debug"
)

// GoSafe starts a recoverable goroutine
func GoSafe(goroutine func()) {
	go func() {
		defer recoverGoroutine()
		goroutine()
	}()
}

func recoverGoroutine() {
	if err := recover(); err != nil {
		log.Println(err)
		debug.PrintStack()
	}
}
