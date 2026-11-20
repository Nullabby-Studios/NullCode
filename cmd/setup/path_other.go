//go:build !windows

package main

import "fmt"

func addToPath(dir string) error {
	fmt.Printf("add %q to your PATH\n", dir)
	return nil
}

func removeFromPath(dir string) error {
	fmt.Printf("remove %q from your PATH\n", dir)
	return nil
}
