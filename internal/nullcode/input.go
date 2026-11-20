package nullcode

import (
	"bufio"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

type key struct {
	typ  string
	r    rune
	text string
}

func readKeys(r io.Reader, out chan<- key) {
	br := bufio.NewReaderSize(r, 4096)
	bytesCh := make(chan byte, 512)
	go func() {
		for {
			b, err := br.ReadByte()
			if err != nil {
				close(bytesCh)
				return
			}
			bytesCh <- b
		}
	}()

	readByte := func(timeout time.Duration) (byte, bool) {
		if timeout <= 0 {
			b, ok := <-bytesCh
			return b, ok
		}
		t := time.NewTimer(timeout)
		defer t.Stop()
		select {
		case b, ok := <-bytesCh:
			return b, ok
		case <-t.C:
			return 0, false
		}
	}
	send := func(k key) bool {
		select {
		case out <- k:
			return true
		default:
			// Drop a key rather than block the reader forever.
			return true
		}
	}

	var pending *byte
	next := func(timeout time.Duration) (byte, bool) {
		if pending != nil {
			b := *pending
			pending = nil
			return b, true
		}
		return readByte(timeout)
	}

	for {
		b, ok := next(0)
		if !ok {
			send(key{typ: "ctrl-d"})
			return
		}
		switch {
		case b == 0x1b:
			b2, ok := next(25 * time.Millisecond)
			if !ok {
				send(key{typ: "esc"})
				continue
			}
			if b2 != '[' && b2 != 'O' {
				pending = &b2
				send(key{typ: "esc"})
				continue
			}
			seq := []byte{b2}
			var final byte
			for {
				c, ok := next(50 * time.Millisecond)
				if !ok {
					break
				}
				seq = append(seq, c)
				if c >= 0x40 && c <= 0x7e {
					final = c
					break
				}
				if len(seq) > 16 {
					break
				}
			}
			body := string(seq[1:])
			if strings.HasPrefix(body, "200~") {
				// Bracketed paste: collect until ESC [ 201 ~.
				var paste []byte
				for {
					c, ok := next(200 * time.Millisecond)
					if !ok {
						break
					}
					paste = append(paste, c)
					if len(paste) >= 6 && string(paste[len(paste)-6:]) == "\x1b[201~" {
						paste = paste[:len(paste)-6]
						break
					}
				}
				send(key{typ: "paste", text: string(paste)})
				continue
			}
			if strings.HasPrefix(body, "<") && strings.HasSuffix(body, "M") {
				// SGR mouse report: ESC [ < b ; x ; y M — 64 wheel up, 65 down.
				parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(body, "<"), "M"), ";")
				if len(parts) >= 3 {
					switch parts[0] {
					case "64":
						send(key{typ: "wheel-up"})
					case "65":
						send(key{typ: "wheel-down"})
					}
				}
				continue
			}
			switch {
			case final == 'A':
				send(key{typ: "up"})
			case final == 'B':
				send(key{typ: "down"})
			case final == 'C':
				send(key{typ: "right"})
			case final == 'D':
				send(key{typ: "left"})
			case final == 'H':
				send(key{typ: "home"})
			case final == 'F':
				send(key{typ: "end"})
			case body == "1~" || body == "7~":
				send(key{typ: "home"})
			case body == "4~" || body == "8~":
				send(key{typ: "end"})
			case body == "3~":
				send(key{typ: "delete"})
			case body == "5~":
				send(key{typ: "pgup"})
			case body == "6~":
				send(key{typ: "pgdn"})
			}
		case b == 3:
			send(key{typ: "ctrl-c"})
		case b == 4:
			send(key{typ: "ctrl-d"})
		case b == 12:
			send(key{typ: "ctrl-l"})
		case b == 1:
			send(key{typ: "ctrl-a"})
		case b == 5:
			send(key{typ: "ctrl-e"})
		case b == 21:
			send(key{typ: "ctrl-u"})
		case b == 11:
			send(key{typ: "ctrl-k"})
		case b == 23:
			send(key{typ: "ctrl-w"})
		case b == 15:
			send(key{typ: "ctrl-o"})
		case b == 13 || b == 10:
			send(key{typ: "enter"})
		case b == 127 || b == 8:
			send(key{typ: "backspace"})
		case b == 9:
			send(key{typ: "tab"})
		case b < 32:
			// ignore other control bytes
		default:
			buf := []byte{b}
			need := utf8Len(b) - 1
			for i := 0; i < need; i++ {
				c, ok := next(15 * time.Millisecond)
				if !ok {
					break
				}
				buf = append(buf, c)
			}
			r, _ := utf8.DecodeRune(buf)
			send(key{typ: "rune", r: r})
		}
	}
}

func utf8Len(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b>>5 == 0x6:
		return 2
	case b>>4 == 0xe:
		return 3
	case b>>3 == 0x1e:
		return 4
	default:
		return 1
	}
}
