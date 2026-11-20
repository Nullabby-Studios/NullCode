// Command setup installs NullCode on this machine: it copies the embedded
// nullcode binary into a per-user install directory, adds that directory to
// the user PATH, and prints how to start it. Use -uninstall to remove it.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed payload/nullcode.exe
var payload []byte

const binName = "nullcode.exe"

func defaultDir() string {
	if la := os.Getenv("LocalAppData"); la != "" {
		return filepath.Join(la, "Programs", "NullCode")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".nullcode")
}

func main() {
	dir := flag.String("dir", "", `install directory (default %LOCALAPPDATA%\Programs\NullCode)`)
	rm := flag.Bool("uninstall", false, "remove the installed binary and PATH entry")
	flag.Parse()
	target := *dir
	if target == "" {
		target = defaultDir()
	}
	var err error
	if *rm {
		err = uninstall(target)
	} else {
		err = install(target)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(1)
	}
}

func install(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	bin := filepath.Join(dir, binName)
	if err := os.WriteFile(bin, payload, 0o755); err != nil {
		return err
	}
	if err := addToPath(dir); err != nil {
		return err
	}
	fmt.Printf("NullCode installed to %s\n", bin)
	fmt.Println("Open a NEW terminal, then run:  nullcode")
	fmt.Println("First launch asks for your provider token (saved for next time).")
	return nil
}

func uninstall(dir string) error {
	bin := filepath.Join(dir, binName)
	if err := os.Remove(bin); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := removeFromPath(dir); err != nil {
		return err
	}
	fmt.Printf("NullCode removed (%s)\n", bin)
	return nil
}
